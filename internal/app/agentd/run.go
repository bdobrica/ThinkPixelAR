package agentd

import (
	"context"
	"log/slog"
)

// Run owns the configured supervisor process lifetime. Until AGD-020 composes
// authenticated transport, it remains dormant and never opens a listener or
// launches a harness. Configured is explicitly not connected or harness-ready.
func Run(ctx context.Context, c Config, logger *slog.Logger) error {
	if c.Validate() != nil || logger == nil {
		return ErrConfig
	}
	p, err := NewProcesses(c)
	if err != nil {
		return err
	}
	logger.Info("agent supervisor configured", "component", "thinkpixel-agentd", "state", "awaiting_transport")
	return RunProcesses(ctx, p, nil, logger)
}

// RunProcesses binds an admitted controller to the supervisor lifetime. It never
// launches work. The binary uses an idle controller until AGD-020 supplies dispatch.
func RunProcesses(ctx context.Context, p *Processes, hook InterruptHandler, logger *slog.Logger) error {
	if p == nil || logger == nil {
		return ErrConfig
	}
	<-ctx.Done()
	if err := p.Shutdown(context.Background(), hook); err != nil {
		logger.Error("agent supervisor cleanup failed", "component", "thinkpixel-agentd", "process_state", p.Status().State.String())
		return err
	}
	logger.Info("agent supervisor stopped", "component", "thinkpixel-agentd", "process_state", p.Status().State.String())
	return nil
}
