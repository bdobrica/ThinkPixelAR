package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/config"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func testServer(t *testing.T, ready Readiness, handler stdhttp.Handler, logger *slog.Logger, tracerOptions ...sdktrace.TracerProviderOption) *Server {
	t.Helper()
	provider := sdktrace.NewTracerProvider(tracerOptions...)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	server, err := NewServer(Options{Config: config.Default().HTTP, Clock: clock.Fixed{Time: time.UnixMilli(1700000000000)}, Logger: logger, Tracer: provider.Tracer("test"), Metrics: telemetry.NewMetrics(), Ready: ready, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestHealthMetricsAndSecurityHeaders(t *testing.T) {
	server := testServer(t, nil, nil, nil)
	for _, path := range []string{"/livez", "/readyz", "/metrics"} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(stdhttp.MethodGet, path, nil))
		if recorder.Code != stdhttp.StatusOK {
			t.Errorf("%s status = %d", path, recorder.Code)
		}
		if recorder.Header().Get("X-Request-ID") == "" || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s missing baseline headers", path)
		}
	}
}

func TestReadinessFailureIsCoarseProblem(t *testing.T) {
	const canary = "database-password-canary"
	server := testServer(t, func(context.Context) error { return errors.New(canary) }, nil, nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(stdhttp.MethodGet, "/readyz", nil))
	if recorder.Code != stdhttp.StatusServiceUnavailable || recorder.Header().Get("Content-Type") != "application/problem+json" || strings.Contains(recorder.Body.String(), canary) || !strings.Contains(recorder.Body.String(), `"code":"temporarily-unavailable"`) {
		t.Fatalf("unsafe readiness response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRequestIDValidationAndPropagation(t *testing.T) {
	handler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		_, _ = io.WriteString(w, RequestIDFromContext(r.Context()))
	})
	server := testServer(t, nil, handler, nil)
	for _, tc := range []struct {
		supplied  string
		preserved bool
	}{{"client-id_42", true}, {"Bearer secret", false}, {"", false}} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(stdhttp.MethodGet, "/v1/test", nil)
		request.Header.Set(requestIDHeader, tc.supplied)
		server.Handler().ServeHTTP(recorder, request)
		got := recorder.Header().Get(requestIDHeader)
		if tc.preserved && got != tc.supplied {
			t.Errorf("valid request ID replaced: %q", got)
		}
		if !tc.preserved && (got == "" || got == tc.supplied) {
			t.Errorf("invalid request ID retained: %q", got)
		}
		if recorder.Body.String() != got {
			t.Errorf("context ID = %q, header = %q", recorder.Body.String(), got)
		}
	}
}

func TestTraceContextExtractionAndInvalidDiscard(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	server := testServer(t, nil, stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}), nil, sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	requests := []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-invalid-secret-00f067aa0ba902b7-01"}
	for _, parent := range requests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/x", nil)
		request.Header.Set("traceparent", parent)
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Header().Get("traceparent") == "" {
			t.Error("missing response traceparent")
		}
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	if spans[0].Parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || spans[1].Parent.IsValid() {
		t.Fatalf("trace parents = %s, %v", spans[0].Parent.TraceID(), spans[1].Parent)
	}
	if len(spans[0].Attributes) != 0 || len(spans[1].Attributes) != 0 {
		t.Fatal("unexpected trace attributes")
	}
}

func TestLimitsAndCompressedBodies(t *testing.T) {
	handler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) { _, _ = io.Copy(io.Discard, r.Body) })
	server := testServer(t, nil, handler, nil)
	tests := []struct {
		name    string
		request *stdhttp.Request
		status  int
		code    string
	}{
		{"uri", httptest.NewRequest("GET", "/"+strings.Repeat("x", maxURIBytes), nil), 414, "uri-too-long"},
		{"body", httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", maxRequestBody+1))), 413, "body-too-large"},
		{"chunked-body", func() *stdhttp.Request {
			r := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", maxRequestBody+1)))
			r.ContentLength = -1
			return r
		}(), 413, "body-too-large"},
		{"encoding", func() *stdhttp.Request {
			r := httptest.NewRequest("POST", "/", nil)
			r.Header.Set("Content-Encoding", "gzip")
			return r
		}(), 415, "unsupported-media-type"},
		{"header", func() *stdhttp.Request {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Large", strings.Repeat("x", maxHeaderValue+1))
			return r
		}(), 431, "headers-too-large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, tc.request)
			if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestConfiguredTimeoutsAndGracefulShutdown(t *testing.T) {
	cfg := config.Default().HTTP
	server := testServer(t, nil, nil, nil)
	if server.http.ReadHeaderTimeout != cfg.ReadHeaderTimeout.Duration() || server.http.ReadTimeout != cfg.ReadTimeout.Duration() || server.http.WriteTimeout != cfg.WriteTimeout.Duration() || server.http.IdleTimeout != cfg.IdleTimeout.Duration() {
		t.Fatal("configured HTTP timeouts were not applied")
	}
	listener := &blockingListener{closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("graceful shutdown timed out")
	}
}

type blockingListener struct {
	closed chan struct{}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, stdhttp.ErrServerClosed
}
func (l *blockingListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}
func (l *blockingListener) Addr() net.Addr { return testAddr("test") }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }

func TestPanicRecoveryDoesNotLeakPanic(t *testing.T) {
	const canary = "panic-secret-canary"
	var logs bytes.Buffer
	logger := telemetry.NewJSONLogger(&logs, telemetry.LogOptions{Secrets: []string{canary}})
	server := testServer(t, nil, stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) { panic(canary) }), logger)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/panic", nil))
	if recorder.Code != 500 || strings.Contains(recorder.Body.String(), canary) || strings.Contains(logs.String(), canary) {
		t.Fatalf("panic leaked: response=%s logs=%s", recorder.Body.String(), logs.String())
	}
}
