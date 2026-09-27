package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Only a test double. Production uses postgres.WorkspaceStorage.
type storageMemory struct {
	mu      sync.Mutex
	saved   workspace.Reservation
	deleted bool
	denied  bool
}

func (s *storageMemory) Do(ctx context.Context, c workspace.Command, fn func(context.Context, workspace.Reservation, func(string, string) error) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.denied {
		return workspace.ErrConflict
	}
	if c.Kind == "create" {
		if s.deleted {
			return workspace.ErrConflict
		}
		if s.saved.Request.WorkspaceID == "" {
			s.saved.Request = c.Create
		} else if s.saved.Request != c.Create {
			return workspace.ErrConflict
		}
	}
	if c.Kind == "delete" {
		s.deleted = true
	}
	return fn(ctx, s.saved, func(role, ref string) error {
		if role == "workspace" {
			s.saved.WorkspaceReference = ref
		} else {
			s.saved.StateReference = ref
		}
		return nil
	})
}

type claimAPI struct {
	mu               sync.Mutex
	claims           map[string]*v1.PersistentVolumeClaim
	creates, deletes int
	failState        bool
	loseResponse     bool
}

func (a *claimAPI) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/agents/persistentvolumeclaims/")
	status := func(code int, reason metav1.StatusReason) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Reason: reason, Code: int32(code)})
	}
	switch r.Method {
	case "GET":
		if p := a.claims[name]; p != nil {
			_ = json.NewEncoder(w).Encode(p)
		} else {
			status(404, metav1.StatusReasonNotFound)
		}
	case "POST":
		var p v1.PersistentVolumeClaim
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			status(400, metav1.StatusReasonBadRequest)
			return
		}
		if a.failState && strings.HasPrefix(p.Name, "ar-state-") {
			status(503, metav1.StatusReasonServiceUnavailable)
			return
		}
		if a.claims[p.Name] != nil {
			status(409, metav1.StatusReasonAlreadyExists)
			return
		}
		a.creates++
		p.UID = types.UID(fmt.Sprintf("uid-%d", a.creates))
		p.ResourceVersion = "1"
		p.Status.Phase = v1.ClaimPending
		a.claims[p.Name] = p.DeepCopy()
		if a.loseResponse {
			a.loseResponse = false
			status(504, metav1.StatusReasonTimeout)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(p)
	case "DELETE":
		var opts metav1.DeleteOptions
		_ = json.NewDecoder(r.Body).Decode(&opts)
		p := a.claims[name]
		if p == nil {
			status(404, metav1.StatusReasonNotFound)
			return
		}
		if opts.Preconditions == nil || opts.Preconditions.UID == nil || *opts.Preconditions.UID != p.UID || opts.Preconditions.ResourceVersion == nil || *opts.Preconditions.ResourceVersion != p.ResourceVersion {
			status(409, metav1.StatusReasonConflict)
			return
		}
		a.deletes++
		delete(a.claims, name)
		_ = json.NewEncoder(w).Encode(metav1.Status{Status: "Success"})
	default:
		status(405, metav1.StatusReasonMethodNotAllowed)
	}
}
func storageFixture(t *testing.T) (*Provider, *storageMemory, *claimAPI, workspace.CreateRequest) {
	t.Helper()
	a := &claimAPI{claims: map[string]*v1.PersistentVolumeClaim{}}
	server := httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	cfg := StorageConfig{Namespace: "agents", StorageClass: "csi-test", StorageProfile: "storage-v1", ConfigurationDigest: "sha256:" + strings.Repeat("a", 64), SinglePodWriter: true, Encrypted: true}
	mem := &storageMemory{}
	p, err := NewProvider(client, mem, cfg, func(context.Context, StorageConfig) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	id := func(n int) primitives.ID { return primitives.ID(fmt.Sprintf("01950000-0000-7000-8000-%012d", n)) }
	r := workspace.CreateRequest{TenantID: id(1), SessionID: id(2), WorkspaceID: id(3), Operation: workspace.Operation{ID: id(4)}, StorageProfile: cfg.StorageProfile, ConfigurationDigest: cfg.ConfigurationDigest, CapacityBytes: 1024, StateCapacityBytes: 512, AccessMode: "single-pod-writer", EncryptionRequired: true}
	r.Operation.Digest = workspace.CreateDigest(r)
	return p, mem, a, r
}
func TestStorageProviderLifecycle(t *testing.T) {
	ctx := context.Background()
	p, mem, a, r := storageFixture(t)
	h, err := p.Create(ctx, r)
	if err != nil || h.State != "PENDING" || h.WorkspaceReference == h.StateReference {
		t.Fatal(h, err)
	}
	// New process/client instance uses saved identity, not process-local state.
	p, _ = NewProvider(p.client, mem, p.config, p.qualify)
	if again, err := p.Create(ctx, r); err != nil || again != h {
		t.Fatal(again, err)
	}
	a.mu.Lock()
	for _, claim := range a.claims {
		claim.Status.Phase = v1.ClaimBound
		claim.Spec.VolumeName = "pv-" + claim.Name
		claim.Status.Capacity = claim.Spec.Resources.Requests.DeepCopy()
		if len(claim.OwnerReferences) != 0 || claim.Spec.DataSource != nil {
			t.Fatal("compute-owned or cloned")
		}
	}
	a.mu.Unlock()
	h, err = p.Get(ctx, r.TenantID, r.WorkspaceID)
	if err != nil || h.State != "BOUND" {
		t.Fatal(h, err)
	}
	caps, err := p.Capabilities(ctx)
	if err != nil || !caps.Create || !caps.SinglePodWriter || caps.Snapshot || caps.Clone || caps.Attach {
		t.Fatal(caps, err)
	}
	op := workspace.Operation{ID: "01950000-0000-7000-8000-000000000005"}
	op.Digest = workspace.DeleteDigest(r.TenantID, r.WorkspaceID, op)
	h, err = p.Delete(ctx, r.TenantID, r.WorkspaceID, op)
	if err != nil || h.State != "DELETING" {
		t.Fatal(h, err)
	}
	h, err = p.Delete(ctx, r.TenantID, r.WorkspaceID, op)
	if err != nil || h.State != "ABSENT" {
		t.Fatal(h, err)
	}
	if _, err = p.Create(ctx, r); err == nil {
		t.Fatal("resurrected deleted storage")
	}
	if a.creates != 2 || a.deletes != 2 {
		t.Fatal(a.creates, a.deletes)
	}
}
func TestStorageProviderPartialAndAmbiguousCreate(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			p, mem, a, r := storageFixture(t)
			ctx := context.Background()
			a.failState = !lost
			a.loseResponse = lost
			if _, err := p.Create(ctx, r); err == nil {
				t.Fatal("expected ambiguous/partial failure")
			}
			if !lost && mem.saved.WorkspaceReference == "" {
				t.Fatal("lost first UID")
			}
			a.mu.Lock()
			a.failState = false
			a.mu.Unlock()
			p, _ = NewProvider(p.client, mem, p.config, p.qualify)
			if _, err := p.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			if a.creates != 2 {
				t.Fatal("duplicate allocation", a.creates)
			}
		})
	}
}
func TestStorageProviderRejectsDriftAndMissing(t *testing.T) {
	for _, which := range []string{"uid", "owner", "class", "source", "digest", "capacity", "mode", "missing", "qualification", "denied", "request"} {
		t.Run(which, func(t *testing.T) {
			p, mem, a, r := storageFixture(t)
			ctx := context.Background()
			if _, err := p.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			a.mu.Lock()
			name := "ar-workspace-" + string(r.WorkspaceID)
			claim := a.claims[name]
			switch which {
			case "uid":
				claim.UID = "replacement"
			case "owner":
				claim.OwnerReferences = []metav1.OwnerReference{{UID: "sandbox"}}
			case "class":
				*claim.Spec.StorageClassName = "other"
			case "source":
				claim.Spec.DataSource = &v1.TypedLocalObjectReference{Kind: "PersistentVolumeClaim", Name: "other"}
			case "digest":
				claim.Annotations["thinkpixel.io/create-digest"] = "other"
			case "capacity":
				claim.Spec.Resources.Requests[v1.ResourceStorage] = claim.Spec.Resources.Requests[v1.ResourceStorage].DeepCopy()
				q := claim.Spec.Resources.Requests[v1.ResourceStorage]
				q.Set(2048)
				claim.Spec.Resources.Requests[v1.ResourceStorage] = q
			case "mode":
				claim.Spec.AccessModes = []v1.PersistentVolumeAccessMode{v1.ReadWriteMany}
			case "missing":
				delete(a.claims, name)
			case "qualification":
				p.qualify = func(context.Context, StorageConfig) error { return errors.New("secret-canary") }
			case "denied":
				mem.denied = true
			case "request":
				r.CapacityBytes = 2048
				r.Operation.Digest = workspace.CreateDigest(r)
			}
			a.mu.Unlock()
			if _, err := p.Create(ctx, r); err == nil || strings.Contains(err.Error(), "secret-canary") {
				t.Fatal(err)
			}
			if a.creates != 2 {
				t.Fatal("reallocated storage")
			}
		})
	}
}
func TestStorageDeleteRejectsReplacement(t *testing.T) {
	p, _, a, r := storageFixture(t)
	ctx := context.Background()
	if _, err := p.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.claims["ar-workspace-"+string(r.WorkspaceID)].UID = "replacement"
	a.mu.Unlock()
	op := workspace.Operation{ID: "01950000-0000-7000-8000-000000000005"}
	op.Digest = workspace.DeleteDigest(r.TenantID, r.WorkspaceID, op)
	if _, err := p.Delete(ctx, r.TenantID, r.WorkspaceID, op); !errors.Is(err, workspace.ErrIntegrity) {
		t.Fatal(err)
	}
	if a.deletes != 0 {
		t.Fatal("deleted replacement")
	}
}

func TestStorageConcurrentCreate(t *testing.T) {
	p, _, a, r := storageFixture(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Create(context.Background(), r); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if a.creates != 2 {
		t.Fatal("concurrent duplicate allocation", a.creates)
	}
}
