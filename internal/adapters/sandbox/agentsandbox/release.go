package agentsandbox

import (
	"context"
	"errors"
	"strconv"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func LifecycleDigest(tenant, id primitives.ID, kind, operationID string) string {
	return sandbox.LifecycleDigest(tenant, id, kind, operationID)
}
func (p *KubernetesAgentSandboxProvider) begin(ctx context.Context, tenant, id primitives.ID, kind string, op sandbox.Operation) (uint64, error) {
	if op.ID == "" || len(op.ID) > 128 || op.Digest != LifecycleDigest(tenant, id, kind, op.ID) {
		return 0, sandbox.ErrInvalid
	}
	if _, err := p.bindings.Get(ctx, tenant, id); err != nil {
		return 0, err
	}
	store, ok := p.bindings.(sandbox.LifecycleStore)
	if !ok {
		return 0, sandbox.ErrUnsupported
	}
	revision, err := store.BeginOperation(ctx, tenant, id, kind, op)
	if err != nil {
		return 0, err
	}
	if revision == 0 {
		return 0, sandbox.ErrIntegrity
	}
	return revision, nil
}
func (p *KubernetesAgentSandboxProvider) Release(ctx context.Context, tenant, id primitives.ID, op sandbox.Operation) error {
	revision, err := p.begin(ctx, tenant, id, "release", op)
	if err != nil {
		return err
	}
	_, sb, err := p.getOwned(ctx, tenant, id)
	if errors.Is(err, sandbox.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = checkRevision(sb.Annotations, revision); err != nil {
		return err
	}
	// Exact UID and resourceVersion protect against replacement or concurrent
	// mutation between read and delete. Foreground deletion retains evidence until
	// owned compute is gone; acceptance is not physical release confirmation.
	foreground := metav1.DeletePropagationForeground
	uid, rv := sb.UID, sb.ResourceVersion
	err = p.client.Resource(sandboxResource).Namespace(p.namespace).Delete(ctx, sb.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &foreground})
	if err != nil && !errors.Is(providerError(err), sandbox.ErrNotFound) {
		return providerError(err)
	}
	return nil
}
func checkRevision(annotations map[string]string, revision uint64) error {
	if raw := annotations["thinkpixel.io/operation-revision"]; raw != "" {
		observed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || observed > revision {
			return sandbox.ErrConflict
		}
	}
	return nil
}
