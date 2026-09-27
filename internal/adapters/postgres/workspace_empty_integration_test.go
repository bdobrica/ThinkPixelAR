package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/adapters/postgres"
	kube "github.com/bdobrica/ThinkPixelAR/internal/adapters/workspace/kubernetes"
	app "github.com/bdobrica/ThinkPixelAR/internal/app/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestEmptyWorkspaceReconciliation(t *testing.T) {
	url := os.Getenv("THINKPIXELAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("THINKPIXELAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	ids := concurrencyIDs(t, now, 5)
	seedConcurrencySession(t, db, ids[0], ids[1], now, false)
	r := workspace.CreateRequest{TenantID: ids[0], SessionID: ids[1], WorkspaceID: ids[2], Operation: workspace.Operation{ID: ids[3]}, StorageProfile: "test", ConfigurationDigest: testDigest('c'), CapacityBytes: 1024, StateCapacityBytes: 512, AccessMode: "single-pod-writer"}
	r.Operation.Digest = workspace.CreateDigest(r)
	claims := map[string]*v1.PersistentVolumeClaim{}
	var pod *v1.Pod
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		absent := func() {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(metav1.Status{Code: 404, Reason: metav1.StatusReasonNotFound})
		}
		if strings.Contains(req.URL.Path, "persistentvolumeclaims") {
			name := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
			if req.Method == "POST" {
				p := &v1.PersistentVolumeClaim{}
				_ = json.NewDecoder(req.Body).Decode(p)
				p.UID = types.UID("uid-" + p.Name)
				p.ResourceVersion = "1"
				p.Status.Phase = v1.ClaimPending
				claims[p.Name] = p
				_ = json.NewEncoder(w).Encode(p)
				return
			}
			if p := claims[name]; p != nil {
				_ = json.NewEncoder(w).Encode(p)
			} else {
				absent()
			}
			return
		}
		switch req.Method {
		case "POST":
			pod = &v1.Pod{}
			_ = json.NewDecoder(req.Body).Decode(pod)
			pod.UID = "init-uid"
			pod.ResourceVersion = "1"
			creates++
			_ = json.NewEncoder(w).Encode(pod)
		case "GET":
			if pod == nil {
				absent()
			} else {
				_ = json.NewEncoder(w).Encode(pod)
			}
		case "DELETE":
			var options metav1.DeleteOptions
			_ = json.NewDecoder(req.Body).Decode(&options)
			if options.Preconditions == nil || *options.Preconditions.UID != pod.UID {
				w.WriteHeader(409)
				return
			}
			pod = nil
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Success"}`))
		}
	}))
	defer server.Close()
	client, _ := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	allow := func(context.Context, workspace.Command) error { return nil }
	compose := func() (*app.EmptyCreator, *postgres.WorkspaceStorage) {
		s, _ := postgres.NewWorkspaceStorage(db, allow)
		p, e := kube.NewProvider(client, s, kube.StorageConfig{Namespace: "agents", StorageClass: "test", StorageProfile: r.StorageProfile, ConfigurationDigest: r.ConfigurationDigest, SinglePodWriter: true}, func(context.Context, kube.StorageConfig) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		i, e := kube.NewEmptyInitializer(p, kube.EmptyConfig{Image: "registry.invalid/init@" + testDigest('a'), RuntimeClass: "kata", UID: 1000, GID: 1000}, func(context.Context, kube.EmptyConfig) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		c, e := app.NewEmptyCreator(s, p, i)
		if e != nil {
			t.Fatal(e)
		}
		return c, s
	}
	c, s := compose()
	if ready, e := c.Reconcile(ctx, r); e != nil || ready || pod == nil {
		t.Fatal(ready, e)
	}
	var state string
	var generations int
	check := func(want string, n int) {
		t.Helper()
		if e := db.QueryRow(`SELECT state FROM workspaces WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		if e := db.QueryRow(`SELECT count(*) FROM workspace_generations WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&generations); e != nil || state != want || generations != n {
			t.Fatal(state, generations, e)
		}
	}
	check("PROVISIONING", 0)
	// Multiple workers serialize through PostgreSQL, sharing one initialization Pod.
	for _, err := range concurrently(4, func(int) error { _, err := c.Reconcile(ctx, r); return err }) {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A competing reconciliation reuses the same consumer while claims are pending.
	c, s = compose()
	if ready, e := c.Reconcile(ctx, r); e != nil || ready || creates != 1 {
		t.Fatal(ready, e, creates)
	}
	for _, p := range claims {
		p.Status.Phase = v1.ClaimBound
		p.Spec.VolumeName = "pv-" + p.Name
		p.Status.Capacity = p.Spec.Resources.Requests.DeepCopy()
	}
	pod.Spec.NodeName = "worker"
	pod.Status.Phase = v1.PodSucceeded
	pod.Status.ContainerStatuses = []v1.ContainerStatus{{Name: "initialize", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}}}
	if ready, e := c.Reconcile(ctx, r); e != nil || ready || pod != nil {
		t.Fatal(ready, e)
	}
	check("PROVISIONING", 0)
	cmd := workspace.Command{Kind: "initialize", TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, Create: r, Operation: r.Operation}
	failed := errors.New("rollback after ready observation")
	if e := s.Do(ctx, cmd, func(_ context.Context, saved workspace.Reservation, bind func(string, string) error) error {
		if saved.EmptyProof == nil {
			t.Fatal("lost proof")
		}
		if e := bind("empty-ready", "ready"); e != nil {
			return e
		}
		return failed
	}); !errors.Is(e, failed) {
		t.Fatal(e)
	}
	check("PROVISIONING", 0)
	c, s = compose()
	if ready, e := c.Reconcile(ctx, r); e != nil || !ready {
		t.Fatal(ready, e)
	}
	check("READY", 1)
	if ready, e := c.Reconcile(ctx, r); e != nil || !ready || creates != 1 || len(claims) != 2 {
		t.Fatal(ready, e)
	}
	check("READY", 1)
	changed := r
	changed.StateCapacityBytes++
	changed.Operation.Digest = workspace.CreateDigest(changed)
	if _, e := s.ReserveEmpty(ctx, changed); !errors.Is(e, workspace.ErrConflict) {
		t.Fatal(e)
	}
	foreign := r
	foreign.TenantID = ids[4]
	foreign.Operation.Digest = workspace.CreateDigest(foreign)
	if _, e := s.ReserveEmpty(ctx, foreign); !errors.Is(e, workspace.ErrNotFound) {
		t.Fatal(e)
	}
	denied, _ := postgres.NewWorkspaceStorage(db, func(context.Context, workspace.Command) error { return errors.New("secret-canary") })
	if _, e := denied.ReserveEmpty(ctx, r); !errors.Is(e, workspace.ErrUnavailable) {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE workspace_storage_operations SET initializer_reference='replacement' WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID); e == nil {
		t.Fatal("mutable initializer UID")
	}
	var generation int
	var root string
	if e := db.QueryRow(`SELECT generation,integrity_root FROM workspace_generations WHERE tenant_id=$1 AND workspace_id=$2`, r.TenantID, r.WorkspaceID).Scan(&generation, &root); e != nil || generation != 0 || root != workspace.EmptyManifestDigest() {
		t.Fatal(generation, root, e)
	}
	// Lost worker acknowledgement remains replayable after Session readiness.
	if _, e := db.Exec(`UPDATE sessions SET state='READY',state_version=state_version+1,updated_at=CURRENT_TIMESTAMP WHERE tenant_id=$1 AND session_id=$2`, r.TenantID, r.SessionID); e != nil {
		t.Fatal(e)
	}
	if ready, e := c.Reconcile(ctx, r); e != nil || !ready || creates != 1 {
		t.Fatal(ready, e)
	}

}
