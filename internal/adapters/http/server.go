package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	stdhttp "net/http"
	"regexp"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/config"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxURIBytes     = 8 << 10
	maxHeaderBytes  = 32 << 10
	maxHeaderFields = 100
	maxHeaderValue  = 8 << 10
	maxRequestBody  = 256 << 10
	requestIDHeader = "X-Request-ID"
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type contextKey uint8

const requestIDKey contextKey = iota

// Readiness performs a coarse readiness check. Its error is never returned.
type Readiness func(context.Context) error

// Options supplies adapter dependencies.
type Options struct {
	Config  config.HTTPConfig
	Clock   clock.Clock
	Logger  *slog.Logger
	Tracer  trace.Tracer
	Metrics *telemetry.Metrics
	Ready   Readiness
	Handler stdhttp.Handler
}

// Server owns the hardened HTTP server and graceful shutdown lifecycle.
type Server struct {
	http            *stdhttp.Server
	shutdownTimeout time.Duration
}

// NewServer constructs health, metrics, and application routing middleware.
func NewServer(o Options) (*Server, error) {
	if o.Clock == nil {
		return nil, errors.New("http server clock is required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Ready == nil {
		o.Ready = func(context.Context) error { return nil }
	}
	if o.Handler == nil {
		o.Handler = stdhttp.NotFoundHandler()
	}
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /livez", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) { writeJSON(w, stdhttp.StatusOK, `{"status":"ok"}`) })
	mux.HandleFunc("GET /readyz", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if err := o.Ready(r.Context()); err != nil {
			writeProblem(w, r, stdhttp.StatusServiceUnavailable, "temporarily-unavailable", "Service Unavailable")
			return
		}
		writeJSON(w, stdhttp.StatusOK, `{"status":"ok"}`)
	})
	if o.Metrics != nil {
		mux.Handle("GET /metrics", o.Metrics.Handler())
	}
	mux.Handle("/", o.Handler)
	handler := requestContext(o, requestLimits(mux))
	return &Server{http: &stdhttp.Server{
		Addr: o.Config.ListenAddress, Handler: handler,
		ReadHeaderTimeout: o.Config.ReadHeaderTimeout.Duration(), ReadTimeout: o.Config.ReadTimeout.Duration(),
		WriteTimeout: o.Config.WriteTimeout.Duration(), IdleTimeout: o.Config.IdleTimeout.Duration(), MaxHeaderBytes: maxHeaderBytes,
	}, shutdownTimeout: o.Config.ShutdownTimeout.Duration()}, nil
}

// Handler exposes the composed handler for embedding and tests.
func (s *Server) Handler() stdhttp.Handler { return s.http.Handler }

// Serve runs until the listener fails or ctx is cancelled.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	result := make(chan error, 1)
	go func() { result <- s.http.Serve(listener) }()
	select {
	case err := <-result:
		if errors.Is(err, stdhttp.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := s.http.Shutdown(shutdownCtx); err != nil {
			_ = s.http.Close()
			return err
		}
		err := <-result
		if errors.Is(err, stdhttp.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func requestContext(o Options, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		requestID := r.Header.Get(requestIDHeader)
		if !validRequestID.MatchString(requestID) {
			generated, err := primitives.NewID(o.Clock.Now())
			if err != nil {
				writeProblem(w, r, stdhttp.StatusInternalServerError, "internal-error", "Internal Server Error")
				return
			}
			requestID = string(generated)
		}
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(r.Header))
		var span trace.Span
		if o.Tracer != nil {
			ctx, span = o.Tracer.Start(ctx, r.Method+" "+routeName(r.URL.Path), trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
		}
		r = r.WithContext(ctx)
		w.Header().Set(requestIDHeader, requestID)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if span != nil && span.SpanContext().IsValid() {
			propagation.TraceContext{}.Inject(trace.ContextWithSpanContext(context.Background(), span.SpanContext()), propagation.HeaderCarrier(w.Header()))
		}
		defer func() {
			if recover() != nil {
				o.Logger.ErrorContext(ctx, "http handler panic", "request_id", requestID)
				writeProblem(w, r, stdhttp.StatusInternalServerError, "internal-error", "Internal Server Error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestLimits(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if len(r.RequestURI) > maxURIBytes {
			writeProblem(w, r, stdhttp.StatusRequestURITooLong, "uri-too-long", "URI Too Long")
			return
		}
		fields, headerBytes := 0, 0
		for name, values := range r.Header {
			fields += len(values)
			headerBytes += len(name)
			for _, value := range values {
				headerBytes += len(value)
				if len(value) > maxHeaderValue {
					writeProblem(w, r, stdhttp.StatusRequestHeaderFieldsTooLarge, "headers-too-large", "Request Header Fields Too Large")
					return
				}
			}
		}
		if fields > maxHeaderFields || headerBytes > maxHeaderBytes {
			writeProblem(w, r, stdhttp.StatusRequestHeaderFieldsTooLarge, "headers-too-large", "Request Header Fields Too Large")
			return
		}
		if r.Header.Get("Content-Encoding") != "" {
			writeProblem(w, r, stdhttp.StatusUnsupportedMediaType, "unsupported-media-type", "Unsupported Media Type")
			return
		}
		if r.ContentLength > maxRequestBody {
			writeProblem(w, r, stdhttp.StatusRequestEntityTooLarge, "body-too-large", "Content Too Large")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody+1))
		if err != nil {
			writeProblem(w, r, stdhttp.StatusBadRequest, "invalid-request", "Invalid Request")
			return
		}
		if len(body) > maxRequestBody {
			writeProblem(w, r, stdhttp.StatusRequestEntityTooLarge, "body-too-large", "Content Too Large")
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// RequestIDFromContext returns the validated server correlation identifier.
func RequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func routeName(path string) string {
	switch path {
	case "/livez", "/readyz", "/metrics":
		return path
	default:
		return "unmatched"
	}
}

func writeJSON(w stdhttp.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body+"\n")
}
