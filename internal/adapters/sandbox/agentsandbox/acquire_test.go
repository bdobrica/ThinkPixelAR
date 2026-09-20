package agentsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeprofile"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

type testBindings struct {
	mu         sync.Mutex
	b          *sandbox.Binding
	fail       bool
	revision   uint64
	operations map[string]testOperation
}

func (s *testBindings) Reserve(_ context.Context, r sandbox.AcquireRequest) (sandbox.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return sandbox.Binding{}, sandbox.ErrUnavailable
	}
	if s.b != nil {
		if !reflect.DeepEqual(s.b.Request, r) {
			return sandbox.Binding{}, sandbox.ErrConflict
		}
		return *s.b, nil
	}
	s.b = &sandbox.Binding{Request: r}
	return *s.b, nil
}
func (s *testBindings) Get(_ context.Context, tenant, id primitives.ID) (sandbox.Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b == nil || s.b.Request.Scope.TenantID != tenant || s.b.Request.Scope.SandboxID != id {
		return sandbox.Binding{}, sandbox.ErrNotFound
	}
	return *s.b, nil
}
func (s *testBindings) BindReference(_ context.Context, tenant, id primitives.ID, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b == nil || s.b.Request.Scope.TenantID != tenant || s.b.Request.Scope.SandboxID != id {
		return sandbox.ErrNotFound
	}
	if s.b.ProviderReference != "" && s.b.ProviderReference != ref {
		return sandbox.ErrConflict
	}
	s.b.ProviderReference = ref
	return nil
}

func acquireFixture(t *testing.T) sandbox.AcquireRequest {
	t.Helper()
	id := primitives.ID("01900000-0000-7000-8000-000000000001")
	r := sandbox.AcquireRequest{Scope: sandbox.Scope{TenantID: id, SessionID: id, ExecutionID: id, AttemptID: id, SandboxID: id, Generation: 1, AttemptOrdinal: 1}, Operation: sandbox.Operation{ID: "acquire-1"}, Runtime: sandbox.Runtime{Image: "registry.invalid/runtime@sha256:" + strings.Repeat("a", 64), Architecture: "amd64", Entrypoint: []string{"/agentd"}}, ProfileDigest: "sha256:" + strings.Repeat("b", 64), ImplementationDigest: "sha256:" + strings.Repeat("c", 64), Workspace: sandbox.Attachment{Reference: "workspace-reference"}, BootstrapReference: "bootstrap-reference", Deadline: time.Now().UTC().Add(time.Hour).Truncate(time.Second)}
	b, e := os.ReadFile("../../../../docs/profiles/coding-medium-secure.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &r.Profile); e != nil {
		t.Fatal(e)
	}
	r.Operation.Digest, _ = RequestDigest(r)
	return r
}
func testBlueprint(_ context.Context, r sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
	raw, err := json.Marshal(r.Profile)
	if err != nil {
		return core.SandboxBlueprint{}, err
	}
	config := CodingTemplateConfig{References: r.Profile.Implementation, RuntimeClass: "test-kata", NodeSelector: map[string]string{"test": "qualified"}, UserID: 65532, GroupID: 65532, TempBytes: 1 << 30, QualificationDigest: "sha256:" + strings.Repeat("d", 64)}
	template, err := NewCodingTemplate(raw, config, func(runtimeprofile.Profile, CodingTemplateConfig) error { return nil })
	if err != nil {
		return core.SandboxBlueprint{}, err
	}
	r.Profile, _, r.ProfileDigest, _, r.ImplementationDigest = template.Resolution()
	r.Workspace.MountPath = "/workspace"
	mapped, err := template.Render(r, CodingVolumes{AttachmentReference: r.Workspace.Reference, BootstrapReference: r.BootstrapReference, WorkspaceClaim: "workspace", StateClaim: "state", BootstrapSecret: "bootstrap"})
	if err != nil {
		return core.SandboxBlueprint{}, err
	}
	return mapped.Spec.SandboxBlueprint, nil
}

type testAPI struct {
	mu            sync.Mutex
	object        map[string]any
	pod           map[string]any
	unavailable   bool
	creates       int
	loseResponse  bool
	deletes       int
	patches       int
	deleteOptions metav1.DeleteOptions
}

func (a *testAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		if a.unavailable {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"ServiceUnavailable","code":503}`))
			return
		}
		if strings.Contains(r.URL.Path, "/pods/") {
			if a.pod == nil {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`))
				return
			}
			_ = json.NewEncoder(w).Encode(a.pod)
			return
		}
		if a.object == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"NotFound","code":404}`))
			return
		}
		_ = json.NewEncoder(w).Encode(a.object)
		return
	}
	if r.Method == "PATCH" {
		if a.object == nil {
			w.WriteHeader(404)
			return
		}
		var patch map[string]any
		_ = json.NewDecoder(r.Body).Decode(&patch)
		pm := patch["metadata"].(map[string]any)
		m := a.object["metadata"].(map[string]any)
		if pm["uid"] != m["uid"] || pm["resourceVersion"] != m["resourceVersion"] {
			w.WriteHeader(409)
			return
		}
		a.patches++
		m["resourceVersion"] = strconv.Itoa(a.patches + 1)
		for k, v := range pm["annotations"].(map[string]any) {
			m["annotations"].(map[string]any)[k] = v
		}
		a.object["spec"].(map[string]any)["operatingMode"] = patch["spec"].(map[string]any)["operatingMode"]
		_ = json.NewEncoder(w).Encode(a.object)
		return
	}
	if r.Method == "DELETE" {
		if a.object == nil {
			w.WriteHeader(404)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&a.deleteOptions)
		a.deletes++
		a.object = nil
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Success"}`))
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if a.object != nil {
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"AlreadyExists","code":409}`))
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&a.object); err != nil {
		w.WriteHeader(400)
		return
	}
	a.creates++
	a.object["metadata"].(map[string]any)["uid"] = "provider-uid-1"
	a.object["metadata"].(map[string]any)["resourceVersion"] = "1"
	if a.loseResponse {
		a.loseResponse = false
		w.WriteHeader(504)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"Timeout","code":504}`))
		return
	}
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(a.object)
}
func providerFixture(t *testing.T, a *testAPI, b *testBindings) *KubernetesAgentSandboxProvider {
	t.Helper()
	server := httptest.NewServer(a)
	t.Cleanup(server.Close)
	c, e := dynamic.NewForConfig(&rest.Config{Host: server.URL, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	p, e := New(c, b, "sandboxes", testBlueprint, WithNetworkEnforcer(func(context.Context, sandbox.AcquireRequest, string) error { return nil }))
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestAcquireReplayConcurrencyAndAmbiguousCreate(t *testing.T) {
	a, b := &testAPI{loseResponse: true}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	if _, e := p.Acquire(context.Background(), r); !errors.Is(e, sandbox.ErrTimeout) {
		t.Fatalf("lost response: %v", e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			h, e := p.Acquire(context.Background(), r)
			if e != nil || h.SandboxID != r.Scope.SandboxID || h.State != sandbox.Provisioning {
				t.Errorf("replay: %+v %v", h, e)
			}
		})
	}
	wg.Wait()
	if a.creates != 1 {
		t.Fatalf("duplicate creates: %d", a.creates)
	}
	replacement := providerFixture(t, a, b)
	if _, e := replacement.Acquire(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	changed := r
	changed.Runtime.Image = "registry.invalid/other@sha256:" + strings.Repeat("d", 64)
	changed.Operation.Digest, _ = RequestDigest(changed)
	if _, e := p.Acquire(context.Background(), changed); !errors.Is(e, sandbox.ErrConflict) {
		t.Fatalf("digest reuse: %v", e)
	}
	a.mu.Lock()
	a.object["metadata"].(map[string]any)["uid"] = "replacement-uid"
	a.mu.Unlock()
	if _, e := p.Acquire(context.Background(), r); !errors.Is(e, sandbox.ErrIntegrity) {
		t.Fatalf("UID replacement: %v", e)
	}
}
func TestAcquireReservesBeforeExternalMutation(t *testing.T) {
	a := &testAPI{}
	p := providerFixture(t, a, &testBindings{fail: true})
	r := acquireFixture(t)
	if _, e := p.Acquire(context.Background(), r); e == nil || a.creates != 0 {
		t.Fatal("created without durable reservation")
	}
	r.Runtime.Image = "mutable:latest"
	r.Operation.Digest, _ = RequestDigest(r)
	if _, e := p.Acquire(context.Background(), r); !errors.Is(e, sandbox.ErrInvalid) {
		t.Fatal("mutable image admitted")
	}
}

func TestAcquireRejectsTamperedProviderSpecAndBoundAbsence(t *testing.T) {
	a, b := &testAPI{}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	if _, e := p.Acquire(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	a.mu.Lock()
	a.object["spec"].(map[string]any)["service"] = true
	a.mu.Unlock()
	if _, e := p.Acquire(context.Background(), r); !errors.Is(e, sandbox.ErrIntegrity) {
		t.Fatalf("tampered spec: %v", e)
	}
	a.mu.Lock()
	a.object = nil
	a.mu.Unlock()
	if _, e := p.Acquire(context.Background(), r); !errors.Is(e, sandbox.ErrNotFound) || a.creates != 1 {
		t.Fatalf("recreated an already bound sandbox: %v", e)
	}
}
