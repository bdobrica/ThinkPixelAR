package agentsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

func TestResumePreservesIdentityAndNeverReacquiresMissingCompute(t *testing.T) {
	a, b := &testAPI{}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	ctx := context.Background()
	original, err := p.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	op := lifecycleOperation(r, "resume", "resume-1")
	if _, err = p.Resume(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatal("resumed never-suspended resource")
	}
	if err = p.Suspend(ctx, r.Scope.TenantID, r.Scope.SandboxID, lifecycleOperation(r, "suspend", "suspend-1")); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Suspended", "status": "True", "observedGeneration": int64(0)}}}
	a.mu.Unlock()
	for range 2 {
		h, err := p.Resume(ctx, r.Scope.TenantID, r.Scope.SandboxID, op)
		if err != nil || h.ProviderReference != original.ProviderReference || h.SandboxID != original.SandboxID || h.State != sandbox.Resuming {
			t.Fatalf("resume: %+v %v", h, err)
		}
	}
	if a.patches != 2 || a.creates != 1 {
		t.Fatal("resume created replacement or repeated patch")
	}
	a.mu.Lock()
	a.object = nil
	a.mu.Unlock()
	if _, err = p.Resume(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); !errors.Is(err, sandbox.ErrNotFound) || a.creates != 1 {
		t.Fatal("reacquired missing resource")
	}
}
