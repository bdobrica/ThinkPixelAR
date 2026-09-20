package reconciliation

import (
	"context"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// Compute reconciles the initial cold-acquisition / release-and-restore lane.
// Native process suspension is not Session checkpoint or resume semantics.
type Compute struct {
	store     sandbox.ComputeStore
	provider  sandbox.ComputeProvider
	authority sandbox.ComputeAuthority
}

func NewCompute(store sandbox.ComputeStore, provider sandbox.ComputeProvider, authority sandbox.ComputeAuthority) (*Compute, error) {
	if store == nil || provider == nil || authority == nil {
		return nil, sandbox.ErrInvalid
	}
	return &Compute{store: store, provider: provider, authority: authority}, nil
}

func (c *Compute) Reconcile(ctx context.Context, tenant, id primitives.ID) (sandbox.ComputeObservation, error) {
	intent, err := c.store.LoadCompute(ctx, tenant, id)
	if err != nil {
		return sandbox.ComputeObservation{}, err
	}
	r := intent.Binding.Request
	if r.Scope.TenantID != tenant || r.Scope.SandboxID != id {
		return sandbox.ComputeObservation{}, sandbox.ErrIntegrity
	}
	var observed sandbox.ComputeObservation
	switch intent.Desired {
	case sandbox.ComputeRunning:
		if !intent.Current {
			observed = recovery("FENCED_COMPUTE")
			break
		}
		if err = c.authority.CheckCompute(ctx, r.Scope); err != nil {
			return sandbox.ComputeObservation{}, sandbox.ErrPermission
		}
		observed = c.running(ctx, intent)
	case sandbox.ComputeReleased:
		if !intent.ReleaseAuthorized {
			return sandbox.ComputeObservation{}, sandbox.ErrConflict
		}
		observed = c.release(ctx, intent)
	default:
		return sandbox.ComputeObservation{}, sandbox.ErrUnsupported
	}
	if err = c.store.RecordCompute(ctx, intent, observed); err != nil {
		return sandbox.ComputeObservation{}, err
	}
	return observed, nil
}

func (c *Compute) running(ctx context.Context, intent sandbox.ComputeIntent) sandbox.ComputeObservation {
	r := intent.Binding.Request
	status, err := c.provider.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if errors.Is(err, sandbox.ErrNotFound) {
		if intent.Binding.ProviderReference != "" {
			return recovery("BOUND_COMPUTE_MISSING")
		}
		// Replay the original immutable operation after an ambiguous create. Never
		// allocate a new SandboxID or treat absence as a completed Session.
		if intent.Operation != r.Operation {
			return recovery("ACQUIRE_IDENTITY_MISMATCH")
		}
		handle, e := c.provider.Acquire(ctx, r)
		if e != nil {
			return failure(e)
		}
		if handle.SandboxID != r.Scope.SandboxID || handle.ProviderReference == "" {
			return recovery("PROVIDER_IDENTITY_MISMATCH")
		}
		return sandbox.ComputeObservation{State: sandbox.Provisioning, Code: "ACQUIRE_ACCEPTED"}
	}
	if err != nil {
		return failure(err)
	}
	if status.Handle.SandboxID != r.Scope.SandboxID || (intent.Binding.ProviderReference != "" && status.Handle.ProviderReference != intent.Binding.ProviderReference) {
		return recovery("PROVIDER_IDENTITY_MISMATCH")
	}
	switch status.State {
	case sandbox.Ready:
		if !status.Effective.Verified {
			return recovery("EFFECTIVE_STATE_UNVERIFIED")
		}
		return sandbox.ComputeObservation{State: sandbox.Ready, Code: "COMPUTE_READY", Converged: true, Effective: status.Effective}
	case sandbox.Requested, sandbox.Provisioning, sandbox.Resuming:
		return sandbox.ComputeObservation{State: status.State, Code: "COMPUTE_PENDING"}
	case sandbox.Unknown:
		return sandbox.ComputeObservation{State: sandbox.Unknown, Code: "PROVIDER_UNKNOWN"}
	default:
		return recovery("COMPUTE_UNEXPECTED_STATE")
	}
}

func (c *Compute) release(ctx context.Context, intent sandbox.ComputeIntent) sandbox.ComputeObservation {
	r := intent.Binding.Request
	// Release replays the saved operation, whose provider implementation rechecks
	// cleanup ownership. Acceptance alone never proves compute has disappeared.
	if err := c.provider.Release(ctx, r.Scope.TenantID, r.Scope.SandboxID, intent.Operation); err != nil {
		return failure(err)
	}
	_, err := c.provider.Get(ctx, r.Scope.TenantID, r.Scope.SandboxID)
	if errors.Is(err, sandbox.ErrNotFound) {
		return sandbox.ComputeObservation{State: sandbox.Released, Code: "COMPUTE_ABSENT", Converged: true}
	}
	if err != nil {
		return failure(err)
	}
	return sandbox.ComputeObservation{State: sandbox.Releasing, Code: "RELEASE_PENDING"}
}

func recovery(code string) sandbox.ComputeObservation {
	return sandbox.ComputeObservation{State: sandbox.Unknown, Code: code, RecoveryRequired: true}
}
func failure(err error) sandbox.ComputeObservation {
	switch {
	case errors.Is(err, sandbox.ErrIntegrity), errors.Is(err, sandbox.ErrUnsupported), errors.Is(err, sandbox.ErrInvalid):
		return recovery("PROVIDER_INTEGRITY_FAILURE")
	case errors.Is(err, sandbox.ErrConflict):
		return sandbox.ComputeObservation{State: sandbox.Unknown, Code: "FENCE_CHANGED"}
	case errors.Is(err, sandbox.ErrPermission):
		return sandbox.ComputeObservation{State: sandbox.Unknown, Code: "PROVIDER_PERMISSION"}
	default:
		return sandbox.ComputeObservation{State: sandbox.Unknown, Code: "PROVIDER_UNAVAILABLE"}
	}
}
