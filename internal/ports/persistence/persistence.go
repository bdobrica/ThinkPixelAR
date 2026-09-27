// Package persistence defines tenant-scoped authoritative storage ports.
package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/cleanup"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/idempotency"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/outbox"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrNotFound              = errors.New("persistent resource not found")
	ErrConflict              = errors.New("persistent resource conflict")
	ErrRequestDigestMismatch = errors.New("idempotency request digest mismatch")
)

// TransactionManager runs one tenant's authoritative changes atomically.
type TransactionManager interface {
	WithinTransaction(context.Context, primitives.ID, func(context.Context, Repositories) error) error
}

// Repositories are valid only for the callback that received them.
type Repositories interface {
	LocalGrants() LocalGrantRepository
	Sessions() SessionRepository
	Executions() ExecutionRepository
	Attempts() AttemptRepository
	RuntimeEvents() RuntimeEventRepository
	Idempotency() IdempotencyRepository
	Outbox() OutboxRepository
	Reconciliation() ReconciliationRepository
	Cleanup() CleanupRepository
}

// CleanupRepository persists exact external targets and credential-free
// tombstones. Worker coordination is provided separately by Reconciliation.
type CleanupRepository interface {
	Add(context.Context, *cleanup.Intent) error
	Get(context.Context, primitives.ID) (*cleanup.Intent, error)
	ListDue(context.Context, time.Time, int) ([]*cleanup.Intent, error)
	Update(context.Context, *cleanup.Intent, uint64) error
}

// ReconciliationRepository leases restart-safe work to stateless reconcilers.
type ReconciliationRepository interface {
	Add(context.Context, *reconciliation.Work) error
	Get(context.Context, primitives.ID) (*reconciliation.Work, error)
	ClaimAvailable(context.Context, primitives.ID, time.Time, time.Time, int) ([]*reconciliation.Work, error)
	Update(context.Context, *reconciliation.Work, uint64) error
}

type SessionRepository interface {
	GetForUpdate(context.Context, primitives.ID) (*session.Session, error)
	// Activate atomically sets the current Execution and advances the Session fence.
	Activate(context.Context, *session.Session, uint64, primitives.ID) error
	Add(context.Context, *session.Session) error
	Get(context.Context, primitives.ID) (*session.Session, error)
	Update(context.Context, *session.Session, uint64) error
}

type ExecutionRepository interface {
	GetForUpdate(context.Context, primitives.ID) (*execution.Execution, error)
	// Signal bodies are Confidential; operation IDs are stable delivery identities.
	AddSignal(context.Context, SignalRecord) error
	GetSignal(context.Context, primitives.ID) (SignalRecord, error)
	// Input is Confidential tenant content, excluded from events and outbox payloads.
	AddInput(context.Context, primitives.ID, string, string) error
	GetInput(context.Context, primitives.ID) (string, string, error)
	Add(context.Context, *execution.Execution) error
	Get(context.Context, primitives.ID) (*execution.Execution, error)
	Update(context.Context, *execution.Execution, uint64) error
}

type SignalRecord struct {
	ID          primitives.ID
	ExecutionID primitives.ID
	Payload     []byte
	Digest      string
}

type AttemptRepository interface {
	Add(context.Context, *attempt.Attempt) error
	Get(context.Context, primitives.ID) (*attempt.Attempt, error)
	Update(context.Context, *attempt.Attempt, uint64) error
}

type RuntimeEventRepository interface {
	// ReadNext returns at most one retained event strictly after the cursor,
	// with bounds from the same database snapshot. Latest includes expired history.
	ReadNext(context.Context, primitives.ID, uint64, time.Time) (EventRead, error)
	// NextSequence locks the Session stream until this transaction ends.
	NextSequence(context.Context, primitives.ID) (uint64, error)
	Append(context.Context, *runtimeevent.Event) error
}

type EventRead struct {
	Event            *runtimeevent.Event
	Earliest, Latest uint64
}

// IdempotencyRepository atomically elects one record for a scoped mutation.
type IdempotencyRepository interface {
	Reserve(context.Context, *idempotency.Record) (*idempotency.Record, bool, error)
	Get(context.Context, idempotency.Scope) (*idempotency.Record, error)
	Update(context.Context, *idempotency.Record, uint64) error
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

// OutboxRepository persists publication intent in the aggregate transaction
// and leases available messages to at-least-once dispatchers.
type OutboxRepository interface {
	Add(context.Context, *outbox.Message) error
	Get(context.Context, primitives.ID) (*outbox.Message, error)
	ClaimAvailable(context.Context, primitives.ID, time.Time, time.Time, int) ([]*outbox.Message, error)
	Update(context.Context, *outbox.Message, uint64) error
}
