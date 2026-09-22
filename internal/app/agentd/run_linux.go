package agentd

import (
	"context"
	"log/slog"
)

func runControlled(ctx context.Context, b *TransportBootstrap, logger *slog.Logger) error {
	p, err := NewProcessControl(b.Config())
	if err != nil {
		return err
	}
	logger.Info("agent supervisor connecting", "component", "thinkpixel-agentd")
	p.supervisor = ctx
	err = RunConnections(ctx, b, ConnectionHooks{Check: p.CheckClient, Serve: p.Serve, Disconnected: func(cleanup context.Context) error {
		// Local cancellation uses the one permanent shutdown below (or already
		// started by Serve for reporting), not a second independent stop budget.
		if ctx.Err() != nil {
			return nil
		}
		return p.Disconnected(cleanup)
	}})
	// The immutable projection cannot supply replacement credentials. On failure
	// exit after bounded shutdown; AR must fence and replace this sandbox.
	if stopErr := p.processes.Shutdown(context.Background(), nil); stopErr != nil {
		logger.Error("agent supervisor cleanup failed", "component", "thinkpixel-agentd", "process_state", p.processes.Status().State.String())
		return stopErr
	}
	logger.Info("agent supervisor stopped", "component", "thinkpixel-agentd", "process_state", p.processes.Status().State.String())
	return err
}
