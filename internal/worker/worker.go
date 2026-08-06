// Package worker runs the async execution path: it consumes messages from a
// broker, processes each within a bounded timeout, recovers from handler panics,
// and drains cleanly on shutdown. It is domain-agnostic (foundation): the
// message handler is injected by the caller, so this package never imports a
// business domain. Reference for real SQS/Kafka consumers.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/luisfelipecoelho/go-template/internal/messaging"
)

// Handler processes a single consumed message. The caller injects it, keeping
// domain decoding/dispatch out of this foundation package.
type Handler func(ctx context.Context, msg messaging.Message) error

// Metrics records worker throughput. The concrete OTel implementation lives in
// internal/telemetry; this interface is owned here (the consumer) per Uber style.
type Metrics interface {
	MessageProcessed(ctx context.Context)
	MessageFailed(ctx context.Context)
}

// Worker consumes a topic and processes messages until its context is done.
type Worker struct {
	log      *slog.Logger
	consumer messaging.Consumer
	topic    string
	timeout  time.Duration
	metrics  Metrics
	handler  Handler
}

// New constructs a Worker. The handler is injected by the caller (the
// composition root), so message decoding and dispatch stay in the app/business
// layers — this foundation package only provides the timeout + recovery loop.
func New(log *slog.Logger, consumer messaging.Consumer, topic string, timeout time.Duration, m Metrics, h Handler) *Worker {
	return &Worker{
		log:      log,
		consumer: consumer,
		topic:    topic,
		timeout:  timeout,
		metrics:  m,
		handler:  h,
	}
}

// Run consumes until ctx is done or the bus closes, then returns nil (clean
// drain). Each message is processed with a per-message timeout and panic
// recovery, so a single poison message never stops the loop.
//
// Goroutine ownership: Run drives the consume loop on the caller's goroutine;
// it terminates when ctx is canceled or the consumer returns.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.consumer.Consume(ctx, w.topic, w.process); err != nil {
		return fmt.Errorf("consuming topic %s: %w", w.topic, err)
	}
	return nil
}

// process wraps one message with a timeout and panic recovery, then records the
// outcome. It never returns an error to the consumer: the in-memory bus has no
// redelivery, so failure accounting is done here via metrics + error logs.
func (w *Worker) process(ctx context.Context, msg messaging.Message) (err error) {
	msgCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			w.metrics.MessageFailed(msgCtx)
			w.log.ErrorContext(msgCtx, "worker handler panicked",
				slog.String("topic", msg.Topic),
				slog.String("key", msg.Key),
				slog.Any("panic", r),
			)
			err = nil // recovered: keep the loop alive
		}
	}()

	if hErr := w.handler(msgCtx, msg); hErr != nil {
		w.metrics.MessageFailed(msgCtx)
		w.log.ErrorContext(msgCtx, "worker handler failed",
			slog.String("topic", msg.Topic),
			slog.String("key", msg.Key),
			slog.String("error", hErr.Error()),
		)
		return nil
	}

	w.metrics.MessageProcessed(msgCtx)
	return nil
}
