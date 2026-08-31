// Command thinkpixel-agentd is the sandbox-local harness supervisor.
//
// The Phase 1 baseline only establishes its process and packaging boundary.
// Harness lifecycle and authenticated transport arrive in Phase 4.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
)

func main() {
	logger := telemetry.NewJSONLogger(os.Stderr, telemetry.LogOptions{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	run(ctx, logger)
}

func run(ctx context.Context, logger *slog.Logger) {
	logger.Info("agent supervisor baseline started", "component", "thinkpixel-agentd")
	<-ctx.Done()
	logger.Info("agent supervisor baseline stopped", "component", "thinkpixel-agentd")
}
