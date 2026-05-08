// Package obs provides observability primitives — OpenTelemetry tracer,
// zap logger with trace-id correlation, and chi middleware to emit a
// span per request.
//
// Loop 4 Wave A scaffold. Replaces ad-hoc logging
// in cmd/* binaries with a single configurable surface.
//
// Configuration is env-var driven; defaults match the dispatch:
//
//	OTEL_ENABLED            — "true" / "false". Default true.
//	OTEL_EXPORTER_OTLP_ENDPOINT — gRPC endpoint. Default empty (no-op exporter).
//	OTEL_SAMPLE_RATE        — float in [0, 1]. Default 1.0 in dev, 0.1 in prod.
//	OTEL_SERVICE_NAME       — overrides the serviceName argument if set.
//	ENV                     — "dev" | "staging" | "prod". Used for sample-rate default.
//
// References:
//   - https://opentelemetry.io/docs/specs/otel/
//   - https://github.com/open-telemetry/opentelemetry-go
package obs

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer is the per-service OpenTelemetry tracer plus the cleanup hook
// callers invoke at shutdown.
type Tracer struct {
	Tracer   trace.Tracer
	Shutdown func(context.Context) error
}

// NewTracer initializes the global OpenTelemetry tracer for serviceName.
//
// When OTEL_ENABLED is "false" or OTEL_EXPORTER_OTLP_ENDPOINT is empty,
// returns a no-op tracer with a no-op shutdown — safe to call in tests
// and in environments without a collector.
//
// On success the returned Tracer.Shutdown should be deferred from the
// caller's main() to flush any in-flight spans on exit.
func NewTracer(ctx context.Context, serviceName string) (*Tracer, error) {
	if name := os.Getenv("OTEL_SERVICE_NAME"); name != "" {
		serviceName = name
	}
	if !envBool("OTEL_ENABLED", true) || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return &Tracer{
			Tracer:   otel.Tracer(serviceName),
			Shutdown: func(context.Context) error { return nil },
		}, nil
	}

	exporter, err := otlptrace.New(ctx, otlptracegrpc.NewClient())
	if err != nil {
		return nil, fmt.Errorf("obs: otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
		resource.WithFromEnv(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, fmt.Errorf("obs: resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRate()))),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Tracer{
		Tracer:   provider.Tracer(serviceName),
		Shutdown: provider.Shutdown,
	}, nil
}

// sampleRate returns the configured sampling rate. Default is 1.0 in
// dev, 0.1 in prod / staging — matches the dispatch.
func sampleRate() float64 {
	if v := os.Getenv("OTEL_SAMPLE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			return f
		}
	}
	switch os.Getenv("ENV") {
	case "prod", "production", "staging":
		return 0.1
	default:
		return 1.0
	}
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	case "0", "false", "FALSE", "False", "no", "off":
		return false
	default:
		return def
	}
}
