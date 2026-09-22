package sandboxtransport

import (
	"context"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

var ErrCredentialState = errors.New("agentd credential state rejected")

type Connection struct {
	ID       primitives.ID
	Epoch    uint64
	Deadline time.Time
}

// CredentialRegistry is durable identity bookkeeping, not provider/Run authority.
// Trusted composition must check current provider and authority before each call.
// Implementations independently enforce the persisted Session/Attempt fence.
type CredentialRegistry interface {
	Version(context.Context, Identity) (uint64, error)
	Register(context.Context, CredentialRequest, CredentialGrant, CredentialRecord) error
	// Reconnect atomically replaces the epoch using the latest unexpired credential.
	// Bootstrap credentials must have been consumed; predecessor credentials fail.
	Reconnect(context.Context, Peer, time.Time) (Connection, error)
	ConsumeBootstrap(context.Context, Peer, []byte, time.Time) (Connection, error)
	CheckConnection(context.Context, Peer, Connection) error
	CloseConnection(context.Context, Identity, Connection) error
}
