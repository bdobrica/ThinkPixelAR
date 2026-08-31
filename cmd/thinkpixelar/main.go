package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	httpadapter "github.com/bdobrica/ThinkPixelAR/internal/adapters/http"
	"github.com/bdobrica/ThinkPixelAR/internal/config"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
)

func main() {
	logger := telemetry.NewJSONLogger(os.Stderr, telemetry.LogOptions{})
	if err := run(logger); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(config.Options{})
	if err != nil {
		return err
	}
	tracing, err := telemetry.NewTracing(telemetry.TraceOptions{ServiceName: "thinkpixelar", Environment: string(cfg.Environment), SetGlobal: true})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer func() { _ = tracing.Shutdown(context.Background()) }()
	server, err := httpadapter.NewServer(httpadapter.Options{Config: cfg.HTTP, Clock: clock.UTC{}, Logger: logger, Tracer: tracing.Tracer("thinkpixelar/http"), Metrics: telemetry.NewMetrics()})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.HTTP.ListenAddress)
	if err != nil {
		return err
	}
	logger.Info("http server started", "address", listener.Addr().String())
	if err := server.Serve(ctx, listener); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
