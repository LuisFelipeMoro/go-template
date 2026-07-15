// Package telemetry bootstraps OpenTelemetry traces and metrics over OTLP gRPC.
// Init returns the constructed providers so the rest of the app receives them by
// injection (see NewHTTPMetrics / NewWorkerMetrics and the otelgin wiring) —
// nothing here reaches for a global to discover its dependencies. The global
// providers are still set so zero-config third-party instrumentation
// (otelhttp, ...) works, but that is a convenience, not how this package's own
// code obtains its MeterProvider. When disabled, no-op providers are returned so
// instruments are cost-free and construction never depends on call order.
//
// Errors-only logging policy (ADR-6): non-error operational signals belong in
// metrics, produced here — never in logs.
package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Config controls telemetry initialization.
type Config struct {
	Enabled     bool
	Endpoint    string // OTLP gRPC host:port
	ServiceName string
	Version     string
	Env         string
	SampleRatio float64
}

// ShutdownFunc flushes and releases telemetry resources. It is always non-nil,
// even when telemetry is disabled.
type ShutdownFunc func(context.Context) error

// Providers holds the telemetry providers to inject into instrumentation. When
// telemetry is disabled they are no-op implementations, so callers construct
// instruments unconditionally with zero overhead.
type Providers struct {
	Tracer   trace.TracerProvider
	Meter    metric.MeterProvider
	Shutdown ShutdownFunc
}

// Init builds the tracer and meter providers and returns them for injection. It
// also sets the process-wide W3C propagator and the global providers (so
// zero-config instrumentation libraries work), but this package's own metric
// recorders take the returned Meter explicitly rather than reading the global.
// When cfg.Enabled is false it returns no-op providers and a nil-safe shutdown.
func Init(ctx context.Context, cfg Config) (*Providers, error) {
	// Propagator is process-wide by design so incoming trace context is honoured
	// even when this service does not export.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	if !cfg.Enabled {
		return &Providers{
			Tracer:   tracenoop.NewTracerProvider(),
			Meter:    metricnoop.NewMeterProvider(),
			Shutdown: func(context.Context) error { return nil },
		}, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			semconv.DeploymentEnvironment(cfg.Env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("building otel resource: %w", err)
	}

	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(cfg.Endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("creating otlp trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)

	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(cfg.Endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("creating otlp metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	// Set globals for zero-config third-party instrumentation; our own code uses
	// the returned providers by injection.
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	return &Providers{
		Tracer: tp,
		Meter:  mp,
		// Flush metrics first, then traces; join any errors.
		Shutdown: func(ctx context.Context) error {
			return errors.Join(mp.Shutdown(ctx), tp.Shutdown(ctx))
		},
	}, nil
}
