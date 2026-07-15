// retry_test.go
package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fastConfig(maxAttempts int) RetryConfig {
	return RetryConfig{MaxAttempts: maxAttempts, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond, Jitter: 0.2}
}

func TestRetry_SucceedsFirstTry(t *testing.T) {
	t.Parallel()
	calls := 0
	got, err := Retry(context.Background(), fastConfig(3), func(ctx context.Context) (string, error) {
		calls++
		return "ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	assert.Equal(t, 1, calls, "success must not retry")
}

func TestRetry_RetriesTransientThenSucceeds(t *testing.T) {
	t.Parallel()
	calls := 0
	got, err := Retry(context.Background(), fastConfig(5), func(ctx context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("transient")
		}
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, 3, calls)
}

func TestRetry_ExhaustsAttempts(t *testing.T) {
	t.Parallel()
	calls := 0
	sentinel := errors.New("always")
	_, err := Retry(context.Background(), fastConfig(3), func(ctx context.Context) (int, error) {
		calls++
		return 0, sentinel
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "last error must be wrapped")
	assert.Equal(t, 3, calls, "must stop at MaxAttempts")
}

func TestRetry_PermanentErrorStopsImmediately(t *testing.T) {
	t.Parallel()
	calls := 0
	sentinel := errors.New("fatal")
	_, err := Retry(context.Background(), fastConfig(5), func(ctx context.Context) (int, error) {
		calls++
		return 0, Permanent(sentinel)
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.True(t, IsPermanent(err))
	assert.Equal(t, 1, calls, "permanent error must not retry")
}

func TestRetry_StopsOnCtxCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := Retry(ctx, RetryConfig{MaxAttempts: 10, BaseDelay: 50 * time.Millisecond, MaxDelay: time.Second}, func(c context.Context) (int, error) {
		calls++
		cancel() // cancel during the first attempt so the backoff wait aborts
		return 0, errors.New("transient")
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "must not attempt again after cancel")
}

func TestRetry_BackoffGrowsAndIsCapped(t *testing.T) {
	t.Parallel()
	var delays []time.Duration
	cfg := RetryConfig{MaxAttempts: 5, BaseDelay: 10 * time.Millisecond, MaxDelay: 40 * time.Millisecond, Jitter: 0}
	cfg.sleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}
	_, err := Retry(context.Background(), cfg, func(ctx context.Context) (int, error) {
		return 0, errors.New("transient")
	})
	require.Error(t, err)
	// 4 waits between 5 attempts: 10, 20, 40, 40 (capped).
	require.Len(t, delays, 4)
	assert.Equal(t, 10*time.Millisecond, delays[0])
	assert.Equal(t, 20*time.Millisecond, delays[1])
	assert.Equal(t, 40*time.Millisecond, delays[2])
	assert.Equal(t, 40*time.Millisecond, delays[3], "delay must cap at MaxDelay")
}

func TestRetry_JitterWithinBounds(t *testing.T) {
	t.Parallel()
	var delays []time.Duration
	cfg := RetryConfig{MaxAttempts: 4, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second, Jitter: 0.5}
	cfg.sleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	}
	_, _ = Retry(context.Background(), cfg, func(ctx context.Context) (int, error) {
		return 0, errors.New("x")
	})
	// base for attempt 1 is 100ms; jitter 0.5 → within [50ms, 150ms].
	require.NotEmpty(t, delays)
	assert.GreaterOrEqual(t, delays[0], 50*time.Millisecond)
	assert.LessOrEqual(t, delays[0], 150*time.Millisecond)
}

func TestRetry_MaxAttemptsOneNeverRetries(t *testing.T) {
	t.Parallel()
	calls := 0
	_, err := Retry(context.Background(), RetryConfig{MaxAttempts: 1}, func(ctx context.Context) (int, error) {
		calls++
		return 0, errors.New("x")
	})
	require.Error(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetry_WorksWithStructType(t *testing.T) {
	t.Parallel()
	type quote struct{ Price int }
	got, err := Retry(context.Background(), fastConfig(2), func(ctx context.Context) (quote, error) {
		return quote{Price: 10}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, quote{Price: 10}, got)
}

func TestIsPermanent_False(t *testing.T) {
	t.Parallel()
	assert.False(t, IsPermanent(errors.New("plain")))
	assert.False(t, IsPermanent(nil))
}
