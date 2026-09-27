package local

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Observability supplies optional trusted sinks. The default logger still emits
// the local-mode startup notice. No grant, request, or raw error is recorded.
type Observability struct {
	Logger  *slog.Logger
	Tracer  trace.Tracer
	Metrics *telemetry.Metrics
}

func (*Authority) Identity() authority.Identity {
	return authority.Identity{Mode: authority.LocalMode, Issuer: authority.LocalIssuer}
}

func (a *Authority) observe(ctx context.Context, operation string) (context.Context, func(error, authority.State)) {
	started := time.Now()
	var span trace.Span
	if a.observability.Tracer != nil {
		ctx, span = a.observability.Tracer.Start(ctx, "authority."+operation, trace.WithAttributes(
			attribute.String("authority_mode", authority.LocalMode),
			attribute.String("authority_issuer", authority.LocalIssuer)))
	}
	return ctx, func(err error, state authority.State) {
		result := "success"
		if err != nil {
			result = "failure"
		}
		if span != nil {
			span.SetAttributes(attribute.String("result", result))
			if err == nil && state != "" {
				span.SetAttributes(attribute.String("authority_state", string(state)))
			}
			span.End()
		}
		if m := a.observability.Metrics; m != nil {
			_ = m.ObserveAuthority(authority.LocalMode, result, time.Since(started))
			if err != nil {
				m.AuthorityFailure()
			}
		}
		fields := []any{"operation", operation, "result", result}
		if err == nil && state != "" {
			fields = append(fields, "authority_state", string(state))
		}
		a.observability.Logger.InfoContext(ctx, "local authority operation", fields...)
	}
}
