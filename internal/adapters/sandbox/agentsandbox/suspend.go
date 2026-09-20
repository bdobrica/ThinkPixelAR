package agentsandbox

import (
	"context"
	"encoding/json"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"strconv"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

// Suspend is invoked only after application checkpoint/suspend guards succeed.
// The upstream mode removes compute but retains the Sandbox and durable volumes;
// it does not preserve a process or renew authority. Get confirms completion.
func (p *KubernetesAgentSandboxProvider) Suspend(ctx context.Context, tenant, id primitives.ID, op sandbox.Operation) error {
	_, err := p.setMode(ctx, tenant, id, "suspend", core.SandboxOperatingModeSuspended, op)
	return err
}
func (p *KubernetesAgentSandboxProvider) setMode(ctx context.Context, tenant, id primitives.ID, kind string, mode core.SandboxOperatingMode, op sandbox.Operation) (sandbox.Handle, error) {
	revision, err := p.begin(ctx, tenant, id, kind, op)
	if err != nil {
		return sandbox.Handle{}, err
	}
	b, sb, err := p.getOwned(ctx, tenant, id)
	if err != nil {
		return sandbox.Handle{}, err
	}
	if b.ProviderReference == "" || sb.DeletionTimestamp != nil {
		return sandbox.Handle{}, sandbox.ErrConflict
	}
	if err = checkRevision(sb.Annotations, revision); err != nil {
		return sandbox.Handle{}, err
	}
	if mode == core.SandboxOperatingModeRunning && sb.Spec.ShutdownTime != nil && !sb.Spec.ShutdownTime.After(p.now()) {
		return sandbox.Handle{}, sandbox.ErrConflict
	}
	if mode == core.SandboxOperatingModeRunning {
		if err := p.checkNetwork(ctx, b.Request); err != nil {
			return sandbox.Handle{}, err
		}
		if sb.Spec.OperatingMode != core.SandboxOperatingModeSuspended && !(sb.Spec.OperatingMode == core.SandboxOperatingModeRunning && sb.Annotations["thinkpixel.io/operation-digest"] == op.Digest) {
			return sandbox.Handle{}, sandbox.ErrConflict
		}
		expected, err := p.resolve(ctx, b.Request)
		if err != nil {
			return sandbox.Handle{}, sandbox.ErrUnsupported
		}
		if !apiequality.Semantic.DeepEqual(expected, sb.Spec.SandboxBlueprint) {
			return sandbox.Handle{}, sandbox.ErrIntegrity
		}
	}
	annotations := map[string]string{"thinkpixel.io/operation-revision": strconv.FormatUint(revision, 10), "thinkpixel.io/operation-digest": op.Digest}
	if sb.Spec.OperatingMode != mode || sb.Annotations["thinkpixel.io/operation-revision"] != annotations["thinkpixel.io/operation-revision"] {
		data, err := json.Marshal(map[string]any{"metadata": map[string]any{"uid": string(sb.UID), "resourceVersion": sb.ResourceVersion, "annotations": annotations}, "spec": map[string]any{"operatingMode": string(mode)}})
		if err != nil {
			return sandbox.Handle{}, sandbox.ErrInvalid
		}
		_, err = p.client.Resource(sandboxResource).Namespace(p.namespace).Patch(ctx, sb.Name, types.MergePatchType, data, metav1.PatchOptions{})
		if err != nil {
			return sandbox.Handle{}, providerError(err)
		}
	}
	state := sandbox.Suspending
	if mode == core.SandboxOperatingModeRunning {
		state = sandbox.Resuming
	}
	return sandbox.Handle{SandboxID: id, ProviderKind: "kubernetes-agent-sandbox", ProviderReference: b.ProviderReference, State: state, ObservedAt: p.now().UTC()}, nil
}
