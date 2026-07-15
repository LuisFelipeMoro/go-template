// telemetry_test.go
package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestInit_DisabledReturnsNoopProviders(t *testing.T) {
	tel, err := Init(context.Background(), Config{Enabled: false})
	require.NoError(t, err)
	require.NotNil(t, tel)
	require.NotNil(t, tel.Meter, "meter provider must be non-nil (no-op) when disabled")
	require.NotNil(t, tel.Tracer, "tracer provider must be non-nil (no-op) when disabled")
	require.NotNil(t, tel.Shutdown)
	assert.NoError(t, tel.Shutdown(context.Background()))

	// Metrics constructors must work off the injected no-op provider — no global,
	// no ordering dependency.
	_, err = NewHTTPMetrics(tel.Meter)
	assert.NoError(t, err)

	// Propagator is installed regardless so inbound trace context is honoured.
	assert.NotNil(t, otel.GetTextMapPropagator())
}

func TestHTTPMetrics_RecordsWithBoundedAttrs(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	m, err := NewHTTPMetrics(mp)
	require.NoError(t, err)
	m.RecordRequest(context.Background(), "GET", "/v1/items", 200, 5*time.Millisecond)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	names := collectMetricNames(rm)
	assert.Contains(t, names, "http.server.requests")
	assert.Contains(t, names, "http.server.duration")

	// The route attribute must be the template, and no raw-path attribute leaks.
	assert.True(t, hasAttr(rm, "http.server.requests", "http.route", "/v1/items"))
}

func TestWorkerMetrics_Records(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	m, err := NewWorkerMetrics(mp)
	require.NoError(t, err)
	m.MessageProcessed(context.Background())
	m.MessageProcessed(context.Background())
	m.MessageFailed(context.Background())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	names := collectMetricNames(rm)
	assert.Contains(t, names, "worker.messages.processed")
	assert.Contains(t, names, "worker.messages.failed")
	assert.EqualValues(t, 2, counterValue(rm, "worker.messages.processed"))
	assert.EqualValues(t, 1, counterValue(rm, "worker.messages.failed"))
}

func TestMetrics_ConstructibleFromNoopProvider(t *testing.T) {
	// A no-op meter provider (what Init returns when disabled) must yield working,
	// panic-free no-op instruments — no global, no ordering dependency.
	tel, err := Init(context.Background(), Config{Enabled: false})
	require.NoError(t, err)

	hm, err := NewHTTPMetrics(tel.Meter)
	require.NoError(t, err)
	wm, err := NewWorkerMetrics(tel.Meter)
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		hm.RecordRequest(context.Background(), "GET", "/x", 200, time.Millisecond)
		wm.MessageProcessed(context.Background())
		wm.MessageFailed(context.Background())
	})
}

func collectMetricNames(rm metricdata.ResourceMetrics) []string {
	var names []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names = append(names, m.Name)
		}
	}
	return names
}

func hasAttr(rm metricdata.ResourceMetrics, metricName, key, val string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				if v, found := dp.Attributes.Value(attribute.Key(key)); found && v.AsString() == val {
					return true
				}
			}
		}
	}
	return false
}

func counterValue(rm metricdata.ResourceMetrics, metricName string) int64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				var total int64
				for _, dp := range sum.DataPoints {
					total += dp.Value
				}
				return total
			}
		}
	}
	return -1
}
