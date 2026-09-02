// Package persistence defines tenant-scoped authoritative storage ports.
package persistence

import (
	"context"
	"errors"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/attempt"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/execution"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/domain/session"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var (
	ErrNotFound = errors.New("persistent resource not found")
	ErrConflict = errors.New("persistent resource conflict")
)

// TransactionManager runs one tenant's authoritative changes atomically.
type TransactionManager interface {
	WithinTransaction(context.Context, primitives.ID, func(context.Context, Repositories) error) error
}

// Repositories are valid only for the callback that received them.
type Repositories interface {
	Sessions() SessionRepository
	Executions() ExecutionRepository
	Attempts() AttemptRepository
	RuntimeEvents() RuntimeEventRepository
}

type SessionRepository interface {
	Add(context.Context, *session.Session) error
	Get(context.Context, primitives.ID) (*session.Session, error)
	Update(context.Context, *session.Session, uint64) error
}

type ExecutionRepository interface {
	Add(context.Context, *execution.Execution) error
	Get(context.Context, primitives.ID) (*execution.Execution, error)
	Update(context.Context, *execution.Execution, uint64) error
}

type AttemptRepository interface {
	Add(context.Context, *attempt.Attempt) error
	Get(context.Context, primitives.ID) (*attempt.Attempt, error)
	Update(context.Context, *attempt.Attempt, uint64) error
}

type RuntimeEventRepository interface {
	Append(context.Context, *runtimeevent.Event) error
}
