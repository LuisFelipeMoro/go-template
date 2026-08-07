// worker_metrics.go
package telemetry

import (
	"context"

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

	processed, err := newInt64Counter(meter, "worker.messages.processed", "Count of messages processed successfully.", "{message}")
	if err != nil {
		return nil, err
	}

	failed, err := newInt64Counter(meter, "worker.messages.failed", "Count of messages that failed processing.", "{message}")
	if err != nil {
		return nil, err
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
