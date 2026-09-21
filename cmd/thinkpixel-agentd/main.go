// Command thinkpixel-agentd is the sandbox-local harness supervisor.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bdobrica/ThinkPixelAR/internal/app/agentd"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
)

func main() {
	logger := telemetry.NewJSONLogger(os.Stderr, telemetry.LogOptions{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := entry(ctx, os.Args[1:], logger)
	stop()
	os.Exit(code)
}
func entry(ctx context.Context, args []string, logger *slog.Logger) int {
	if len(args) != 0 || agentd.CheckCredentialExposure() != nil {
		logger.Error("agent supervisor startup rejected")
		return 1
	}
	config, err := agentd.Load()
	if err != nil {
		logger.Error("agent supervisor configuration rejected")
		return 1
	}
	if agentd.Run(ctx, config, logger) != nil {
		logger.Error("agent supervisor failed")
		return 1
	}
	return 0
}
