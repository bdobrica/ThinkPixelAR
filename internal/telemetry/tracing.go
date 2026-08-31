package telemetry

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

var safeResourceValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

// TraceOptions configures the OpenTelemetry SDK. No exporter means spans stay
// process-local and are discarded. Initialization never installs automatic
// instrumentation or records request, response, environment, or payload data.
type TraceOptions struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	Exporter       sdktrace.SpanExporter
	Sampler        sdktrace.Sampler
	SetGlobal      bool
}

// Tracing owns a provider and its shutdown lifecycle.
type Tracing struct {
	provider *sdktrace.TracerProvider
}

// NewTracing initializes an OpenTelemetry provider containing only validated,
// low-risk service metadata. Instrumentation must add attributes explicitly
// and remains subject to the data-classification allowlist.
func NewTracing(options TraceOptions) (*Tracing, error) {
	if !safeResourceValue.MatchString(options.ServiceName) {
		return nil, errors.New("trace service name must be a bounded safe identifier")
	}
	attributes := []attribute.KeyValue{semconv.ServiceName(options.ServiceName)}
	if options.ServiceVersion != "" {
		if !safeResourceValue.MatchString(options.ServiceVersion) {
			return nil, errors.New("trace service version must be a bounded safe identifier")
		}
		attributes = append(attributes, semconv.ServiceVersion(options.ServiceVersion))
	}
	if options.Environment != "" {
		if !safeResourceValue.MatchString(options.Environment) {
			return nil, errors.New("trace environment must be a bounded safe identifier")
		}
		attributes = append(attributes, attribute.String("deployment.environment.name", options.Environment))
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attributes...))
	if err != nil {
		return nil, errors.New("initialize trace resource")
	}
	providerOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if options.Sampler != nil {
		providerOptions = append(providerOptions, sdktrace.WithSampler(options.Sampler))
	}
	if options.Exporter != nil {
		providerOptions = append(providerOptions, sdktrace.WithBatcher(options.Exporter, sdktrace.WithBatchTimeout(time.Second)))
	}
	provider := sdktrace.NewTracerProvider(providerOptions...)
	if options.SetGlobal {
		otel.SetTracerProvider(provider)
	}
	return &Tracing{provider: provider}, nil
}

// Tracer returns a named tracer. Callers must use reviewed span names and only
// the approved trace fields documented by the data-classification contract.
func (t *Tracing) Tracer(instrumentationName string) trace.Tracer {
	return t.provider.Tracer(instrumentationName)
}

// ForceFlush exports all ended spans known to the provider.
func (t *Tracing) ForceFlush(ctx context.Context) error { return t.provider.ForceFlush(ctx) }

// Shutdown flushes pending spans and releases exporter resources.
func (t *Tracing) Shutdown(ctx context.Context) error { return t.provider.Shutdown(ctx) }
