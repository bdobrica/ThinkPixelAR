package recovery

import (
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/app/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// SandboxReplacement durably admits one safe replacement, then uses the normal
// acquisition reconciler. Errors/response loss never allocate another candidate.
type SandboxReplacement struct {
	store   sandbox.ReplacementStore
	compute *reconciliation.Compute
}

func NewSandboxReplacement(store sandbox.ReplacementStore, compute *reconciliation.Compute) (*SandboxReplacement, error) {
	if store == nil || compute == nil {
		return nil, sandbox.ErrInvalid
	}
	return &SandboxReplacement{store, compute}, nil
}

func (r *SandboxReplacement) Reconcile(ctx context.Context, tenant, lost primitives.ID, resources sandbox.ReplacementResources) (sandbox.Binding, sandbox.ComputeObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	binding, err := r.store.ReplaceDeleted(ctx, tenant, lost, resources)
	if err != nil {
		return sandbox.Binding{}, sandbox.ComputeObservation{}, err
	}
	observed, err := r.compute.Reconcile(ctx, tenant, binding.Request.Scope.SandboxID)
	return binding, observed, err
}
