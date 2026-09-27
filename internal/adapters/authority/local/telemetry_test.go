package local

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	httpadapter "github.com/bdobrica/ThinkPixelAR/internal/adapters/http"
	"github.com/bdobrica/ThinkPixelAR/internal/config"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/authority"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/clock"
	"github.com/bdobrica/ThinkPixelAR/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLocalAuthorityVisibility(t *testing.T) {
	c, registry, _, caller, request, now := fixture(t)
	var logs bytes.Buffer
	logger := telemetry.NewJSONLogger(&logs, telemetry.LogOptions{})
	metrics := telemetry.NewMetrics()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	a, err := New(c, registry, unavailable{}, clock.Fixed{Time: now}, Observability{Logger: logger, Tracer: provider.Tracer("test"), Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpadapter.NewServer(httpadapter.Options{Config: config.Default().HTTP, Clock: clock.Fixed{Time: now}, Logger: logger, Metrics: metrics, Authority: a})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/authorityz?authority_mode=thinkpixelag", nil)
	req.Header.Set("X-Authority-Mode", "thinkpixelag")
	server.Handler().ServeHTTP(response, req)
	var diagnostic struct {
		authority.Identity
		Description string
	}
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || diagnostic.Identity != a.Identity() || !strings.Contains(diagnostic.Description, "no ThinkPixelAG governance") {
		t.Fatalf("diagnostic: %d %s", response.Code, response.Body)
	}
	ctx := context.Background()
	if _, err := a.Admit(ctx, caller, request); err != authority.ErrUnavailable {
		t.Fatal(err)
	}
	// Untrusted grant metadata cannot relabel failures or escape to any sink.
	canary := "private-grant-canary"
	g := authority.Grant{Mode: "thinkpixelag", Issuer: canary}
	if _, err := a.Validate(ctx, caller, g); err != authority.ErrInvalidGrant {
		t.Fatal(err)
	}
	if err := a.Cancel(ctx, caller, g); err != authority.ErrInvalidGrant {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %d", len(spans))
	}
	for _, span := range spans {
		attrs := map[string]string{}
		for _, a := range span.Attributes {
			attrs[string(a.Key)] = a.Value.AsString()
		}
		if attrs["authority_mode"] != "local" || attrs["authority_issuer"] != authority.LocalIssuer || attrs["result"] != "failure" || len(attrs) != 3 {
			t.Fatalf("unsafe span attributes: %v", attrs)
		}
	}
	if !strings.Contains(logs.String(), "no ThinkPixelAG governance") {
		t.Fatal("missing startup notice")
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["authority_mode"] != "local" || record["authority_issuer"] != authority.LocalIssuer {
			t.Fatalf("unlabeled record: %s", line)
		}
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(response.Body.String(), `thinkpixelar_authority_info{authority_mode="local"} 1`) || !strings.Contains(response.Body.String(), `thinkpixelar_authority_operation_seconds_count{mode="local",result="failure"} 3`) {
		t.Fatalf("missing authority metrics: %s", response.Body)
	}
	for _, forbidden := range []string{canary, "sensitive database detail", string(caller.TenantID), string(request.SessionID), request.RequestDigest, "authority_issuer="} {
		if strings.Contains(response.Body.String(), forbidden) || strings.Contains(logs.String(), forbidden) {
			t.Fatalf("leaked metadata: %s", forbidden)
		}
	}
}
