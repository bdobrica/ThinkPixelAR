package persistence

import (
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// LocalGrantRecord separates immutable issuance bytes from monotonic status.
// Records outlive admission replay retention; there is no snapshot update API.
type LocalGrantRecord struct {
	ID, SessionID primitives.ID
	Snapshot      []byte
	Digest        string
	State         string
	Version       uint64
}

type LocalGrantRepository interface {
	Add(context.Context, LocalGrantRecord) error
	// Get locks the record until the surrounding transaction finishes.
	Get(context.Context, primitives.ID) (LocalGrantRecord, error)
	Transition(context.Context, primitives.ID, uint64, string, time.Time) error
}
