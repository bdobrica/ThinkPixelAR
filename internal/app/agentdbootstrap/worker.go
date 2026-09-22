package agentdbootstrap

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type Sweeper interface {
	Sweep(context.Context, primitives.ID, int) error
}

// Worker has a copied, explicit tenant allowlist; it never discovers tenants or
// lists Kubernetes Secrets. Sweep preserves durable retry/expiry eligibility.
type Worker struct {
	sweep            Sweeper
	tenants          []primitives.ID
	interval, budget time.Duration
	limit            int
	logger           *slog.Logger
	gate             sync.Mutex
}

func NewWorker(s Sweeper, tenants []primitives.ID, interval, budget time.Duration, limit int, logger *slog.Logger) (*Worker, error) {
	if s == nil || logger == nil || len(tenants) == 0 || len(tenants) > 128 || interval < 5*time.Second || interval > time.Minute || budget <= 0 || budget > 30*time.Second || limit < 1 || limit > 128 {
		return nil, ErrBootstrap
	}
	seen := map[primitives.ID]bool{}
	for _, id := range tenants {
		if _, err := primitives.ParseID(string(id)); err != nil || seen[id] {
			return nil, ErrBootstrap
		}
		seen[id] = true
	}
	return &Worker{sweep: s, tenants: slices.Clone(tenants), interval: interval, budget: budget, limit: limit, logger: logger}, nil
}

// Run sweeps immediately, then periodically until cancellation. Each tenant pass
// has its own finite budget. Failures do not terminate the worker or skip another
// tenant, and only a fixed diagnostic is logged. Provider/store calls honor ctx.
func (w *Worker) Run(ctx context.Context) error {
	if !w.gate.TryLock() {
		return ErrBootstrap
	}
	defer w.gate.Unlock()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		for _, tenant := range w.tenants {
			if ctx.Err() != nil {
				return nil
			}
			pass, cancel := context.WithTimeout(ctx, w.budget)
			err := w.sweep.Sweep(pass, tenant, w.limit)
			cancel()
			if err != nil && ctx.Err() == nil {
				w.logger.Warn("agentd bootstrap cleanup pass failed", "tenant_id", string(tenant))
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
