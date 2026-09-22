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
	agentdhost "github.com/bdobrica/ThinkPixelAR/internal/adapters/sandboxtransport/host"
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
	var agentd *agentdhost.Host
	if path, enabled := os.LookupEnv(agentdhost.ConfigEnvironment); enabled {
		kc, err := config.LoadKubernetes(os.LookupEnv)
		if err != nil {
			return agentdhost.ErrHost
		}
		agentd, err = agentdhost.Open(ctx, path, cfg.Database.URL, kc, logger)
		if err != nil {
			return err
		}
		defer agentd.Close()
	}
	server, err := httpadapter.NewServer(httpadapter.Options{Config: cfg.HTTP, Clock: clock.UTC{}, Logger: logger, Tracer: tracing.Tracer("thinkpixelar/http"), Metrics: telemetry.NewMetrics()})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.HTTP.ListenAddress)
	if err != nil {
		return err
	}
	logger.Info("http server started", "address", listener.Addr().String())
	done := make(chan error, 2)
	count := 1
	go func() { done <- server.Serve(ctx, listener) }()
	if agentd != nil {
		count++
		logger.Info("authenticated agentd listener started")
		go func() { done <- agentd.Run(ctx) }()
	}
	var result error
	for range count {
		err := <-done
		stop()
		if err != nil && !errors.Is(err, context.Canceled) && result == nil {
			result = err
		}
	}
	return result
}
