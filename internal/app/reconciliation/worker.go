package reconciliation

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type ComputeReconciler interface {
	Reconcile(context.Context, primitives.ID, primitives.ID) (sandbox.ComputeObservation, error)
}

type Worker struct {
	queue        sandbox.ComputeQueue
	compute      ComputeReconciler
	lease, retry time.Duration
}

func NewWorker(queue sandbox.ComputeQueue, compute ComputeReconciler, lease, retry time.Duration) (*Worker, error) {
	if queue == nil || compute == nil || lease < time.Second || lease > 5*time.Minute || retry < time.Second || retry > time.Hour {
		return nil, sandbox.ErrInvalid
	}
	return &Worker{queue: queue, compute: compute, lease: lease, retry: retry}, nil
}

// RunBatch does not own a daemon loop or a list of authorized tenants. Service
// composition supplies each trusted tenant and a stable worker instance identity.
func (w *Worker) RunBatch(ctx context.Context, tenant, owner primitives.ID, limit int) (int, error) {
	claims, err := w.queue.ClaimCompute(ctx, tenant, owner, w.lease, limit)
	if err != nil {
		return 0, err
	}
	finished := 0
	for _, claim := range claims {
		expires, ok := claim.ClaimExpiresAt()
		if !ok || !expires.After(time.Now()) {
			return finished, sandbox.ErrConflict
		}
		callCtx, cancel := context.WithDeadline(ctx, expires)
		observed, err := w.compute.Reconcile(callCtx, tenant, claim.TargetID())
		cancel()
		code := observed.Code
		done := err == nil && observed.Converged && observed.State == sandbox.Released
		if err != nil {
			code = "RECONCILE_UNAVAILABLE"
			if errors.Is(err, sandbox.ErrConflict) {
				code = "FENCE_CHANGED"
			}
			if errors.Is(err, sandbox.ErrPermission) {
				code = "AUTHORITY_UNAVAILABLE"
			}
			if errors.Is(err, sandbox.ErrUnsupported) {
				code = "UNSUPPORTED_LIFECYCLE"
			}
		}
		// READY is monitored periodically under the same work identity; it is not a
		// completed Session. Only confirmed release terminates this monitoring work.
		if err = w.queue.FinishCompute(ctx, claim, done, code, w.retry); err != nil {
			return finished, err
		}
		finished++
	}
	return finished, nil
}
