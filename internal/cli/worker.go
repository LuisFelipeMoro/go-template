// worker.go
package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	jsonv2 "encoding/json/v2"

	"github.com/luisfelipecoelho/go-template/internal/config"
	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/lifecycle"
	"github.com/luisfelipecoelho/go-template/internal/messaging"
	"github.com/luisfelipecoelho/go-template/internal/telemetry"
	"github.com/luisfelipecoelho/go-template/internal/worker"
)

// perMessageTimeout bounds processing of a single consumed message.
const perMessageTimeout = 30 * time.Second

// newWorkerCmd returns the background worker command.
func newWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Run the background worker",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading worker config: %w", err)
			}
			return runWorker(cmd.Context(), cfg)
		},
	}
}

// runWorker is the composition root for the worker process. The template ships
// with an in-memory bus (nothing external publishes to it), so the worker is a
// runnable skeleton: point messaging.New at a real broker adapter (SQS/Kafka)
// to consume production events. Stop order mirrors the server: worker drains,
// bus closes, telemetry flushes last.
func runWorker(ctx context.Context, cfg config.Config) error {
	obs, err := newObservability(ctx, cfg)
	if err != nil {
		return fmt.Errorf("bootstrapping observability: %w", err)
	}
	log, tel := obs.log, obs.tel

	workerMetrics, err := telemetry.NewWorkerMetrics(tel.Meter)
	if err != nil {
		return fmt.Errorf("building worker metrics: %w", err)
	}

	// The consumer adapter is chosen by MESSAGING_DRIVER; "none" yields a no-op
	// consumer so a worker with no real broker configured still runs cleanly.
	_, consumer, busCloser, err := messaging.New(cfg.Messaging.Driver)
	if err != nil {
		return fmt.Errorf("building bus: %w", err)
	}

	w := worker.New(log, consumer, eventsTopic, perMessageTimeout, workerMetrics, newEventHandler())

	runner := lifecycle.NewRunner(log, cfg.ShutdownTimeout)
	registerWorkerComponents(runner, tel, w, busCloser)

	if err := runner.Run(ctx); err != nil {
		return fmt.Errorf("running worker lifecycle: %w", err)
	}
	return nil
}

// newEventHandler builds the message handler. It is injected at the composition
// root so internal/worker stays domain-agnostic — the worker package owns the
// timeout and recovery loop, never the decoding or the dispatch.
//
// The template decodes and stops there: extend the switch on evt.Type to
// dispatch to a domain service.
func newEventHandler() worker.Handler {
	return func(_ context.Context, msg messaging.Message) error {
		var evt item.Event
		if err := jsonv2.Unmarshal(msg.Payload, &evt); err != nil {
			return fmt.Errorf("decoding event: %w", err)
		}
		return nil
	}
}

// registerWorkerComponents wires the lifecycle graph. Registration order is
// start order and the REVERSE of stop order, mirroring the server: the worker
// drains first, then the bus closes, and telemetry flushes last.
func registerWorkerComponents(
	runner *lifecycle.Runner,
	tel *telemetry.Providers,
	w *worker.Worker,
	busCloser io.Closer,
) {
	runner.Add(lifecycle.Component{
		Name:  "telemetry",
		Start: noStart,
		Stop:  tel.Shutdown,
	})
	runner.Add(lifecycle.Component{
		Name:  "bus",
		Start: noStart,
		Stop:  func(context.Context) error { return busCloser.Close() },
	})
	runner.Add(lifecycle.Component{
		Name:  "worker",
		Start: w.Run,
		Stop:  noStop, // ctx cancel + bus close already drain it
	})
}

// noStop is the Stop of a component that has nothing to release: cancelling the
// run context is the whole of its shutdown.
func noStop(context.Context) error { return nil }
