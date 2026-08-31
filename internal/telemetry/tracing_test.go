package telemetry

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracingExportsOnlyExplicitSafeMetadataByDefault(t *testing.T) {
	const canary = "payload-secret-canary"
	t.Setenv("AUTHORIZATION", "Bearer "+canary)
	exporter := tracetest.NewInMemoryExporter()
	tracing, err := NewTracing(TraceOptions{
		ServiceName:    "thinkpixelar",
		ServiceVersion: "v1.2.3",
		Environment:    "test",
		Exporter:       exporter,
		Sampler:        sdktrace.AlwaysSample(),
	})
	if err != nil {
		t.Fatalf("NewTracing() error = %v", err)
	}
	_, span := tracing.Tracer("test.instrumentation").Start(context.Background(), "session.transition")
	span.End()
	if err := tracing.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush() error = %v", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d", len(spans))
	}
	spanData := spans[0]
	if spanData.Name != "session.transition" {
		t.Fatalf("span name = %q", spanData.Name)
	}
	if len(spanData.Attributes) != 0 {
		t.Fatalf("unexpected span attributes = %#v", spanData.Attributes)
	}
	resourceText := spanData.Resource.String()
	if !strings.Contains(resourceText, "thinkpixelar") || strings.Contains(resourceText, canary) || strings.Contains(resourceText, "Bearer") {
		t.Fatalf("unsafe or incomplete resource = %s", resourceText)
	}
	if err := tracing.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestTracingRejectsSensitiveOrUnboundedResourceValues(t *testing.T) {
	tests := []TraceOptions{
		{},
		{ServiceName: "thinkpixelar", ServiceVersion: "Bearer secret"},
		{ServiceName: "thinkpixelar", Environment: strings.Repeat("x", 129)},
		{ServiceName: "https://user:password@example.test"},
	}
	for index, options := range tests {
		if tracing, err := NewTracing(options); err == nil {
			_ = tracing.Shutdown(context.Background())
			t.Errorf("case %d accepted unsafe resource metadata", index)
		}
	}
}
