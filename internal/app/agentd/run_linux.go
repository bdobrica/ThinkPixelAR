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
	err = RunConnections(ctx, b, ConnectionHooks{Check: p.CheckClient, Serve: p.Serve, Disconnected: p.Disconnected})
	// The immutable projection cannot supply replacement credentials. On failure
	// exit after bounded shutdown; AR must fence and replace this sandbox.
	if stopErr := p.processes.Shutdown(context.Background(), nil); stopErr != nil {
		return stopErr
	}
	logger.Info("agent supervisor stopped", "component", "thinkpixel-agentd", "process_state", p.processes.Status().State.String())
	return err
}
