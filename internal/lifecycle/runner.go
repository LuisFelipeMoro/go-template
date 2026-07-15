// runner.go
// Package lifecycle provides a single place to run a set of long-lived
// components and shut them down gracefully: components stop in reverse
// registration order on SIGINT/SIGTERM or the first fatal start error, bounded
// by a shutdown timeout.
//
// Security (SEC-2): the runner logs component names and error messages only —
// never request payloads or secrets.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// Component is a managed unit of work.
//
// Start blocks until ctx is done or a fatal error occurs; short-lived setup may
// return nil immediately. Stop must be idempotent and is bounded by the shutdown
// context; it may be nil when there is nothing to tear down.
type Component struct {
	Name  string
	Start func(ctx context.Context) error
	Stop  func(ctx context.Context) error
}

// Runner starts components and orchestrates their graceful shutdown. Construct
// it with NewRunner; the zero value is not usable. Run must be called at most
// once per Runner — concurrent or repeated Run calls are not supported.
type Runner struct {
	log     *slog.Logger
	timeout time.Duration
	comps   []Component
}

// NewRunner returns a Runner that bounds total shutdown by shutdownTimeout.
func NewRunner(log *slog.Logger, shutdownTimeout time.Duration) *Runner {
	return &Runner{log: log, timeout: shutdownTimeout}
}

// Add registers a component. Registration order defines start order and the
// reverse of stop order.
func (r *Runner) Add(c Component) {
	r.comps = append(r.comps, c)
}

// Run starts every component and blocks until an incoming SIGINT/SIGTERM, a
// cancellation of ctx, or the first fatal start error. It then stops components
// in reverse registration order under a fresh timeout-bounded context and
// returns the first fatal start error if any, otherwise the joined stop errors.
//
// Goroutine ownership: Run owns every component Start goroutine (via errgroup)
// and each Stop goroutine; all terminate on ctx cancellation, a fatal error, or
// the shutdown timeout.
func (r *Runner) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	group, groupCtx := errgroup.WithContext(ctx)
	for _, c := range r.comps {
		group.Go(func() error {
			if c.Start == nil {
				return nil
			}
			if err := c.Start(groupCtx); err != nil {
				return fmt.Errorf("component %s start: %w", c.Name, err)
			}
			return nil
		})
	}

	startErr := group.Wait()

	stopErr := r.stopAll()

	if startErr != nil {
		return startErr
	}
	return stopErr
}

// stopAll stops components in reverse registration order under a single fresh
// timeout-bounded context (the parent is already cancelled at this point) and
// joins any stop errors.
func (r *Runner) stopAll() error {
	stopCtx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	var errs []error
	for i := len(r.comps) - 1; i >= 0; i-- {
		c := r.comps[i]
		if c.Stop == nil {
			continue
		}
		if err := stopComponent(stopCtx, c); err != nil {
			r.log.Error("component stop failed", slog.String("component", c.Name), slog.String("error", err.Error()))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// stopComponent runs c.Stop bounded by ctx, returning a wrapped error if Stop
// fails or the shutdown deadline elapses first. The Stop goroutine is owned here
// and reports on a buffered channel, so it never blocks even if Stop ignores its
// context and outlives the deadline.
func stopComponent(ctx context.Context, c Component) error {
	done := make(chan error, 1)
	go func() { done <- c.Stop(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("component %s stop: %w", c.Name, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("component %s stop: %w", c.Name, ctx.Err())
	}
}
