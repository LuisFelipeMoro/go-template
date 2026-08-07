// Package resilience provides dependency-free, context-aware building blocks
// for outbound calls: bounded retry with exponential backoff + jitter, and a
// three-state circuit breaker. These are blueprints — swap them for
// sony/gobreaker or cenkalti/backoff if you outgrow them.
package resilience

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// RetryConfig tunes Retry. The zero value is unusable; set MaxAttempts >= 1.
type RetryConfig struct {
	MaxAttempts int           // total attempts, >= 1 (1 = no retry)
	BaseDelay   time.Duration // first backoff; defaults to 100ms
	MaxDelay    time.Duration // backoff cap; defaults to 5s
	Jitter      float64       // fraction of delay randomized, 0..1; defaults to 0.2

	// sleep is an injectable wait seam for tests; nil uses a real ctx-aware timer.
	sleep func(ctx context.Context, d time.Duration) error
}

func (c *RetryConfig) applyDefaults() {
	if c.MaxAttempts < 1 {
		c.MaxAttempts = 1
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 100 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 5 * time.Second
	}
	if c.Jitter < 0 {
		c.Jitter = 0
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
}

// permanentError marks an error as non-retryable.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent wraps err so Retry stops immediately instead of retrying.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err (or anything it wraps) is Permanent-marked.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// Retry runs fn until it succeeds, ctx is done, a Permanent error is returned,
// or attempts are exhausted. It returns the last error wrapped with the attempt
// count on exhaustion.
func Retry[T any](ctx context.Context, cfg RetryConfig, fn func(ctx context.Context) (T, error)) (T, error) {
	cfg.applyDefaults()

	var zero T
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, fmt.Errorf("retry aborted: %w", err)
		}

		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if IsPermanent(err) {
			return zero, err
		}
		if attempt == cfg.MaxAttempts {
			break
		}

		delay := backoff(cfg, attempt)
		if serr := cfg.sleep(ctx, delay); serr != nil {
			return zero, fmt.Errorf("retry wait: %w", errors.Join(serr, lastErr))
		}
	}

	return zero, fmt.Errorf("retry exhausted after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

// backoff computes the capped, jittered delay before the given attempt's retry.
func backoff(cfg RetryConfig, attempt int) time.Duration {
	d := float64(cfg.BaseDelay) * math.Pow(2, float64(attempt-1))
	if d > float64(cfg.MaxDelay) {
		d = float64(cfg.MaxDelay)
	}
	if cfg.Jitter > 0 {
		// Symmetric jitter in [-Jitter, +Jitter] fraction of d.
		delta := d * cfg.Jitter * (2*rand.Float64() - 1) //nolint:gosec // jitter, not security
		d += delta
	}
	if d < 0 {
		d = 0
	}
	return time.Duration(d)
}

// sleepCtx waits d or returns early if ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
