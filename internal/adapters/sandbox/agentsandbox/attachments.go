package agentsandbox

import (
	"context"
	"github.com/bdobrica/ThinkPixelAR/internal/adapters/workspace/kubernetes"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	core "sigs.k8s.io/agent-sandbox/api/v1beta1"
)

// BootstrapLookup returns the immutable Secret name for the exact saved scope
// and protected bootstrap reference. It must not return or log credential bytes.
// Issuance, lifetime and one-time consumption belong to the transport adapter.
type BootstrapLookup func(context.Context, sandbox.Scope, string) (string, error)

// AttachedBlueprintResolver composes read-only attachment/template resolution.
// The Workspace owner must materialize and reserve attachments before Acquire;
// this resolver never performs an external write before sandbox reservation.
func AttachedBlueprintResolver(template *CodingTemplate, reader workspace.AttachmentReader, volumes *kubernetes.AttachmentResolver, bootstrap BootstrapLookup) (BlueprintResolver, error) {
	if template == nil || reader == nil || volumes == nil || bootstrap == nil {
		return nil, sandbox.ErrInvalid
	}
	return func(ctx context.Context, r sandbox.AcquireRequest) (core.SandboxBlueprint, error) {
		a, err := reader.GetAttachment(ctx, r.Scope.TenantID, r.Workspace.Reference)
		if err != nil {
			return core.SandboxBlueprint{}, sandbox.ErrUnavailable
		}
		expected := workspace.AttachmentScope{TenantID: r.Scope.TenantID, SessionID: r.Scope.SessionID, ExecutionID: r.Scope.ExecutionID, AttemptID: r.Scope.AttemptID, SandboxID: r.Scope.SandboxID, WorkspaceID: r.Workspace.WorkspaceID, ExecutionGeneration: r.Scope.Generation, AttemptOrdinal: r.Scope.AttemptOrdinal, WorkspaceGeneration: r.Workspace.Generation}
		if a.Scope != expected || a.Reference != r.Workspace.Reference || a.Reference == "" || a.MountPath != r.Workspace.MountPath || a.ReadOnly != r.Workspace.ReadOnly || !shaDigest.MatchString(a.ConfigurationDigest) || !shaDigest.MatchString(a.RequestDigest) {
			return core.SandboxBlueprint{}, sandbox.ErrIntegrity
		}
		for _, id := range []primitives.ID{a.OperationID, a.Scope.WorkspaceID} {
			if _, err = primitives.ParseID(string(id)); err != nil {
				return core.SandboxBlueprint{}, sandbox.ErrIntegrity
			}
		}
		names, err := volumes.Resolve(ctx, a, r.Profile)
		if err != nil {
			return core.SandboxBlueprint{}, err
		}
		secret, err := bootstrap(ctx, r.Scope, r.BootstrapReference)
		if err != nil {
			return core.SandboxBlueprint{}, sandbox.ErrUnavailable
		}
		mapped, err := template.Render(r, CodingVolumes{AttachmentReference: a.Reference, BootstrapReference: r.BootstrapReference, WorkspaceClaim: names.WorkspaceClaim, StateClaim: names.StateClaim, BootstrapSecret: secret})
		if err != nil {
			return core.SandboxBlueprint{}, err
		}
		return mapped.Spec.SandboxBlueprint, nil
	}, nil
}
