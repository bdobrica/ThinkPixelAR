package agentsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
)

func TestSuspendUsesOperatingModeWithDurableReplay(t *testing.T) {
	a, b := &testAPI{}, &testBindings{}
	p := providerFixture(t, a, b)
	r := acquireFixture(t)
	ctx := context.Background()
	if _, err := p.Acquire(ctx, r); err != nil {
		t.Fatal(err)
	}
	op := lifecycleOperation(r, "suspend", "suspend-1")
	for range 2 {
		if err := p.Suspend(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); err != nil {
			t.Fatal(err)
		}
	}
	if a.patches != 1 || a.object["spec"].(map[string]any)["operatingMode"] != "Suspended" {
		t.Fatal("incorrect upstream suspension")
	}
	release := lifecycleOperation(r, "release", "release-1")
	if err := p.Release(ctx, r.Scope.TenantID, r.Scope.SandboxID, release); err != nil {
		t.Fatal(err)
	}
	if err := p.Suspend(ctx, r.Scope.TenantID, r.Scope.SandboxID, op); !errors.Is(err, sandbox.ErrConflict) {
		t.Fatalf("superseded operation replay: %v", err)
	}
}
