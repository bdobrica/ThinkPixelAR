package agentsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type testOperation struct {
	kind     string
	op       sandbox.Operation
	revision uint64
}

func (s *testBindings) BeginOperation(_ context.Context, tenant, id primitives.ID, kind string, op sandbox.Operation) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b == nil || s.b.Request.Scope.TenantID != tenant || s.b.Request.Scope.SandboxID != id {
		return 0, sandbox.ErrNotFound
	}
	if s.fail {
		return 0, sandbox.ErrUnavailable
	}
	if s.operations == nil {
		s.operations = map[string]testOperation{}
	}
	if previous, ok := s.operations[op.ID]; ok {
		if previous.kind != kind || previous.op != op || previous.revision != s.revision {
			return 0, sandbox.ErrConflict
		}
		return previous.revision, nil
	}
	s.revision++
	s.operations[op.ID] = testOperation{kind, op, s.revision}
	return s.revision, nil
}
func lifecycleOperation(r sandbox.AcquireRequest, kind, id string) sandbox.Operation {
	return sandbox.Operation{ID: id, Digest: LifecycleDigest(r.Scope.TenantID, r.Scope.SandboxID, kind, id)}
}
func TestReleaseExactPreconditionsReplayAndAbsence(t *testing.T) {
	a, b := &testAPI{}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	ctx := context.Background()
	if _, err := p.Acquire(ctx, r); err != nil {
		t.Fatal(err)
	}
	op := lifecycleOperation(r, "release", "release-1")
	for range 2 {
		if err := p.Release(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); err != nil {
			t.Fatal(err)
		}
	}
	if a.deletes != 1 || a.deleteOptions.Preconditions == nil || *a.deleteOptions.Preconditions.UID != "provider-uid-1" || *a.deleteOptions.Preconditions.ResourceVersion != "1" || *a.deleteOptions.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatalf("unsafe delete: %+v", a.deleteOptions)
	}
	if b.b == nil || b.b.ProviderReference == "" {
		t.Fatal("binding/history discarded")
	}
}
func TestReleaseFailsClosedOnOwnershipOrOperationConflict(t *testing.T) {
	a, b := &testAPI{}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	ctx := context.Background()
	if _, err := p.Acquire(ctx, r); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.object["metadata"].(map[string]any)["uid"] = "someone-else"
	a.mu.Unlock()
	op := lifecycleOperation(r, "release", "release-1")
	if err := p.Release(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); !errors.Is(err, sandbox.ErrIntegrity) || a.deletes != 0 {
		t.Fatal("deleted foreign resource")
	}
	op.Digest = "changed"
	if err := p.Release(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("accepted invalid operation")
	}
}
