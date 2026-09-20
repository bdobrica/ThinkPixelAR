package agentsandbox

import (
	"context"
	"reflect"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

// Resume restores the same owned Sandbox identity; it never reacquires missing
// compute or renews an expired lifecycle. Fresh execution authority and harness
// bootstrap remain application/transport responsibilities after verified Get.
func (p *KubernetesAgentSandboxProvider) Resume(ctx context.Context, tenant, id primitives.ID, op sandbox.Operation) (sandbox.Handle, error) {
	b, sb, err := p.getOwned(ctx, tenant, id)
	if err != nil {
		return sandbox.Handle{}, err
	}
	if sb.Spec.OperatingMode != core.SandboxOperatingModeSuspended && !(sb.Spec.OperatingMode == core.SandboxOperatingModeRunning && sb.Annotations["thinkpixel.io/operation-digest"] == op.Digest) {
		return sandbox.Handle{}, sandbox.ErrConflict
	}
	if sb.Spec.OperatingMode == core.SandboxOperatingModeSuspended {
		observed, err := p.Get(ctx, tenant, id)
		if err != nil {
			return sandbox.Handle{}, err
		}
		if observed.State != sandbox.Suspended {
			return sandbox.Handle{}, sandbox.ErrConflict
		}
	}
	expected, err := p.resolve(ctx, b.Request)
	if err != nil {
		return sandbox.Handle{}, sandbox.ErrUnsupported
	}
	if !reflect.DeepEqual(expected, sb.Spec.SandboxBlueprint) {
		return sandbox.Handle{}, sandbox.ErrIntegrity
	}
	return p.setMode(ctx, tenant, id, "resume", core.SandboxOperatingModeRunning, op)
}
