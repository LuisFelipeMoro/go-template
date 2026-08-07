// http_metrics.go
package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// HTTPMetrics records request throughput and latency. It implements the
// transport's Metrics interface. Instruments come from the global meter
// provider, so when telemetry is disabled they are no-ops.
type HTTPMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

// NewHTTPMetrics constructs the HTTP metric instruments from the injected meter
// provider. Pass telemetry.Providers.Meter; a no-op provider yields no-op
// instruments.
func NewHTTPMetrics(mp metric.MeterProvider) (*HTTPMetrics, error) {
	meter := mp.Meter("github.com/luisfelipecoelho/go-template/http")

	requests, err := newInt64Counter(meter, "http.server.requests", "Count of HTTP requests handled.", "{request}")
	if err != nil {
		return nil, err
	}

	duration, err := meter.Float64Histogram(
		"http.server.duration",
		metric.WithDescription("HTTP request duration."),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating http.server.duration histogram: %w", err)
	}

	return &HTTPMetrics{requests: requests, duration: duration}, nil
}

// newInt64Counter constructs a named Int64Counter, wrapping the meter error
// with the instrument name so a construction failure identifies its source.
func newInt64Counter(meter metric.Meter, name, desc, unit string) (metric.Int64Counter, error) {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		return nil, fmt.Errorf("creating %s counter: %w", name, err)
	}
	return c, nil
}

// RecordRequest records one handled request. Route (template) and status bound
// the attribute cardinality; the raw path is never used.
func (m *HTTPMetrics) RecordRequest(ctx context.Context, method, route string, status int, dur time.Duration) {
	attrs := metric.WithAttributes(
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
	)
	m.requests.Add(ctx, 1, attrs)
	m.duration.Record(ctx, float64(dur.Microseconds())/1000.0, attrs)
}
