package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

type emptyAPI struct {
	claims           *claimAPI
	pod              *v1.Pod
	creates, deletes int
	loseResponse     bool
}

func (a *emptyAPI) serve(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "persistentvolumeclaims") {
		a.claims.serve(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case "GET":
		if a.pod == nil {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(metav1.Status{Reason: metav1.StatusReasonNotFound, Code: 404})
			return
		}
		_ = json.NewEncoder(w).Encode(a.pod)
	case "POST":
		a.pod = &v1.Pod{}
		_ = json.NewDecoder(r.Body).Decode(a.pod)
		a.pod.UID = "init-uid"
		a.pod.ResourceVersion = "1"
		a.creates++
		if a.loseResponse {
			a.loseResponse = false
			w.WriteHeader(504)
			_ = json.NewEncoder(w).Encode(metav1.Status{Reason: metav1.StatusReasonTimeout, Code: 504})
			return
		}
		_ = json.NewEncoder(w).Encode(a.pod)
	case "DELETE":
		var options metav1.DeleteOptions
		_ = json.NewDecoder(r.Body).Decode(&options)
		if options.Preconditions == nil || *options.Preconditions.UID != a.pod.UID || *options.Preconditions.ResourceVersion != a.pod.ResourceVersion {
			w.WriteHeader(409)
			return
		}
		a.pod = nil
		a.deletes++
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Success"}`))
	}
}
func emptyFixture(t *testing.T) (*EmptyInitializer, *storageMemory, *emptyAPI, workspace.CreateRequest) {
	p, mem, claims, r := storageFixture(t)
	a := &emptyAPI{claims: claims}
	server := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	p.client = client
	if _, err = p.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	i, err := NewEmptyInitializer(p, EmptyConfig{Image: "registry.invalid/init@sha256:" + strings.Repeat("b", 64), RuntimeClass: "kata", UID: 1000, GID: 1000}, func(context.Context, EmptyConfig) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return i, mem, a, r
}
func (a *emptyAPI) succeed() {
	for _, p := range a.claims.claims {
		p.Status.Phase = v1.ClaimBound
		p.Spec.VolumeName = "pv-" + p.Name
		p.Status.Capacity = p.Spec.Resources.Requests.DeepCopy()
	}
	a.pod.Spec.NodeName = "worker"
	a.pod.Status.Phase = v1.PodSucceeded
	a.pod.Status.ContainerStatuses = []v1.ContainerStatus{{Name: "initialize", State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 0}}}}
}
func TestEmptyInitializationDelayedBindingAndRestart(t *testing.T) {
	i, mem, a, r := emptyFixture(t)
	ctx := context.Background()
	a.loseResponse = true
	if _, err := i.InitializeEmpty(ctx, r); !errors.Is(err, workspace.ErrUnavailable) {
		t.Fatal(err)
	}
	if ready, err := i.InitializeEmpty(ctx, r); err != nil || ready {
		t.Fatal(ready, err)
	}
	if a.creates != 1 || mem.saved.InitializerReference == "" || a.pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "ar-workspace-"+string(r.WorkspaceID) || a.pod.Spec.Containers[0].VolumeMounts[0].MountPath != "/workspace" {
		t.Fatal("missing durable mount/reservation")
	}
	a.succeed()
	if ready, err := i.InitializeEmpty(ctx, r); err != nil || ready || a.deletes != 1 || mem.saved.EmptyProof == nil {
		t.Fatal(ready, err)
	}
	// New adapter and deleted Pod: publish from durable proof, never rerun shell.
	i, _ = NewEmptyInitializer(i.provider, i.config, i.qualify)
	if ready, err := i.InitializeEmpty(ctx, r); err != nil || !ready {
		t.Fatal(ready, err)
	}
	if ready, err := i.InitializeEmpty(ctx, r); err != nil || !ready || a.creates != 1 || a.deletes != 1 {
		t.Fatal(ready, err)
	}
	if len(a.claims.claims) != 2 || a.claims.deletes != 0 {
		t.Fatal("compute cleanup removed durable storage")
	}
}
func TestEmptyInitializationRejectsUnsafeObservations(t *testing.T) {
	for _, name := range []string{"replacement", "missing", "failed", "subpath", "sidecar", "privilege", "unbound-success", "configuration", "qualification"} {
		t.Run(name, func(t *testing.T) {
			i, _, a, r := emptyFixture(t)
			ctx := context.Background()
			if _, err := i.InitializeEmpty(ctx, r); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "replacement":
				a.pod.UID = "another"
			case "missing":
				a.pod = nil
			case "failed":
				a.pod.Status.Phase = v1.PodFailed
			case "subpath":
				a.pod.Spec.Containers[0].VolumeMounts[0].SubPath = "other"
			case "sidecar":
				a.pod.Spec.Containers = append(a.pod.Spec.Containers, v1.Container{Name: "extra"})
			case "privilege":
				yes := true
				a.pod.Spec.Containers[0].SecurityContext.Privileged = &yes
			case "unbound-success":
				a.succeed()
				for _, p := range a.claims.claims {
					p.Status.Phase = v1.ClaimPending
				}
			case "configuration":
				i.config.UID++
			case "qualification":
				i.qualify = func(context.Context, EmptyConfig) error { return errors.New("secret-canary") }
			}
			if ready, err := i.InitializeEmpty(ctx, r); err == nil || ready || strings.Contains(err.Error(), "secret-canary") {
				t.Fatal(ready, err)
			}
			if a.deletes != 0 || a.creates != 1 {
				t.Fatal("unsafe mutation")
			}
		})
	}
}

func TestEmptyInitializationCommandPreservesExistingData(t *testing.T) {
	i, _, _, r := emptyFixture(t)
	root := t.TempDir()
	work := filepath.Join(root, "workspace")
	state := filepath.Join(root, "state")
	for _, dir := range []string{work, state} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := i.pod(r, []string{"workspace", "state"}).Spec.Containers[0].Command[2]
	script = strings.ReplaceAll(strings.ReplaceAll(script, "/workspace", work), "/state", state)
	run := func() error { return exec.Command("/bin/sh", "-ec", script).Run() }
	if err := run(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(work, "\n")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("accepted nonempty storage")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "preserve" {
		t.Fatal("modified existing data", err)
	}
}
