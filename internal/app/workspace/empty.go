package workspace

import (
	"context"

	port "github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

// EmptyCreator reconciles one durable Session provisioning intent. The caller
// retries pending work with the same request; it does not acknowledge it early.
type EmptyCreator struct {
	store       port.EmptyReservationStore
	provider    port.Provider
	initializer port.EmptyInitializer
}

func NewEmptyCreator(s port.EmptyReservationStore, p port.Provider, i port.EmptyInitializer) (*EmptyCreator, error) {
	if s == nil || p == nil || i == nil {
		return nil, port.ErrInvalid
	}
	return &EmptyCreator{s, p, i}, nil
}
func (c *EmptyCreator) Reconcile(ctx context.Context, r port.CreateRequest) (bool, error) {
	ready, err := c.store.ReserveEmpty(ctx, r)
	if err != nil || ready {
		return ready, err
	}
	if _, err = c.provider.Create(ctx, r); err != nil {
		return false, err
	}
	return c.initializer.InitializeEmpty(ctx, r)
}
