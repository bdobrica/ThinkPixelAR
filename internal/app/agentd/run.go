package agentd

import (
	"context"
	"log/slog"
)

// Run owns the configured supervisor process lifetime. Until AGD-003 installs
// authenticated transport, it remains dormant and never opens a listener or
// launches a harness. Configured is explicitly not connected or harness-ready.
func Run(ctx context.Context, c Config, logger *slog.Logger) error {
	if c.Validate() != nil || logger == nil {
		return ErrConfig
	}
	logger.Info("agent supervisor configured", "component", "thinkpixel-agentd", "state", "awaiting_transport")
	<-ctx.Done()
	logger.Info("agent supervisor stopped", "component", "thinkpixel-agentd")
	return nil
}
