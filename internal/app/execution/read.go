package execution

import (
	"context"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ReadAccess checks current disclosure policy for the persisted Session and
// Execution. It is independent of permission to create or continue work.
// Any denial is presented as not found to avoid disclosing resource existence.
type ReadAccess func(context.Context, Caller, primitives.ID, primitives.ID) error

// Status is the public metadata projection of durable Execution state.
// It deliberately excludes input, grant credentials and terminal evidence.
type Status struct {
	View
	RunReference string `json:"run_reference,omitempty"`
}

type Reader struct {
	store  persistence.TransactionManager
	access ReadAccess
}

func NewReader(store persistence.TransactionManager, access ReadAccess) (*Reader, error) {
	if store == nil || access == nil {
		return nil, ErrUnavailable
	}
	return &Reader{store: store, access: access}, nil
}

// Get reads historical state without validating, renewing or issuing authority.
// Expiry/cancellation does not erase history or synthesize a lifecycle transition.
func (r *Reader) Get(ctx context.Context, caller Caller, id primitives.ID) (Status, error) {
	var result Status
	if _, err := primitives.ParseID(string(caller.TenantID)); err != nil || !digestPattern.MatchString(caller.PrincipalDigest) {
		return result, ErrDenied
	}
	if _, err := primitives.ParseID(string(id)); err != nil {
		return result, ErrInvalid
	}
	err := r.store.WithinTransaction(ctx, caller.TenantID, func(ctx context.Context, repos persistence.Repositories) error {
		e, err := repos.Executions().Get(ctx, id)
		if err != nil {
			return err
		}
		if e == nil || e.TenantID() != caller.TenantID || e.ID() != id {
			return ErrNotFound
		}
		b := e.Binding()
		if r.access(ctx, caller, b.SessionID, id) != nil {
			return ErrNotFound
		}
		mode := "local"
		switch b.AuthorityMode {
		case "LOCAL":
		case "THINKPIXEL_AG":
			mode = "thinkpixelag"
		default:
			return ErrUnavailable
		}
		result = Status{View: View{ID: e.ID(), SessionID: b.SessionID, State: e.State(), StateVersion: e.StateVersion(), Generation: b.SessionGeneration, AuthorityMode: mode, AuthorityIssuer: b.AuthorityNamespace, CreatedAt: e.CreatedAt()}, RunReference: b.ExternalRunID}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, persistence.ErrNotFound) {
			return Status{}, ErrNotFound
		}
		return Status{}, ErrUnavailable
	}
	return result, nil
}
