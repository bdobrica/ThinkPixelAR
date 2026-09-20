package sandbox

import (
	"context"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

// ComputeQueue is durable worker coordination, separate from aggregate authority.
// Claim and finish are tenant-scoped and reject expired/superseded claim fences.
type ComputeQueue interface {
	ClaimCompute(context.Context, primitives.ID, primitives.ID, time.Duration, int) ([]*reconciliation.Work, error)
	FinishCompute(context.Context, *reconciliation.Work, bool, string, time.Duration) error
}
