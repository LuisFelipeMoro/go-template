// worker_metrics.go
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// WorkerMetrics records message-processing throughput. It implements the
// worker's Metrics interface.
type WorkerMetrics struct {
	processed metric.Int64Counter
	failed    metric.Int64Counter
}

// NewWorkerMetrics constructs the worker metric instruments from the injected
// meter provider. Pass telemetry.Providers.Meter; a no-op provider yields no-op
// instruments.
func NewWorkerMetrics(mp metric.MeterProvider) (*WorkerMetrics, error) {
	meter := mp.Meter("github.com/luisfelipecoelho/go-template/worker")

	processed, err := meter.Int64Counter(
		"worker.messages.processed",
		metric.WithDescription("Count of messages processed successfully."),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating worker.messages.processed counter: %w", err)
	}

	failed, err := meter.Int64Counter(
		"worker.messages.failed",
		metric.WithDescription("Count of messages that failed processing."),
		metric.WithUnit("{message}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating worker.messages.failed counter: %w", err)
	}

	return &WorkerMetrics{processed: processed, failed: failed}, nil
}

// MessageProcessed records one successfully processed message.
func (m *WorkerMetrics) MessageProcessed(ctx context.Context) {
	m.processed.Add(ctx, 1)
}

// MessageFailed records one failed message.
func (m *WorkerMetrics) MessageFailed(ctx context.Context) {
	m.failed.Add(ctx, 1)
}
