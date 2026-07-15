// breaker_test.go
package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 3, Cooldown: time.Minute, HalfOpenMax: 1})
	failing := func(ctx context.Context) error { return errors.New("boom") }

	for i := 0; i < 3; i++ {
		assert.Error(t, cb.Execute(context.Background(), failing))
	}
	assert.Equal(t, StateOpen, cb.State())

	calls := 0
	err := cb.Execute(context.Background(), func(ctx context.Context) error { calls++; return nil })
	assert.ErrorIs(t, err, ErrOpen, "open breaker must fast-fail")
	assert.Equal(t, 0, calls, "fn must not run while open")
}

func TestBreaker_HalfOpenAfterCooldownThenCloses(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 2, Cooldown: 10 * time.Second, HalfOpenMax: 1})
	cb.now = func() time.Time { return now }

	failing := func(ctx context.Context) error { return errors.New("boom") }
	require.Error(t, cb.Execute(context.Background(), failing))
	require.Error(t, cb.Execute(context.Background(), failing))
	require.Equal(t, StateOpen, cb.State())

	// Advance past the cooldown: next call is a half-open probe.
	now = now.Add(11 * time.Second)
	require.NoError(t, cb.Execute(context.Background(), func(ctx context.Context) error { return nil }))
	assert.Equal(t, StateClosed, cb.State(), "probe success closes the breaker")
}

func TestBreaker_HalfOpenProbeFailureReopens(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1, Cooldown: 5 * time.Second, HalfOpenMax: 1})
	cb.now = func() time.Time { return now }

	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("boom") }))
	require.Equal(t, StateOpen, cb.State())

	now = now.Add(6 * time.Second)
	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("still bad") }))
	assert.Equal(t, StateOpen, cb.State(), "probe failure reopens")
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 3, Cooldown: time.Minute, HalfOpenMax: 1})

	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("x") }))
	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("x") }))
	require.NoError(t, cb.Execute(context.Background(), func(ctx context.Context) error { return nil }))
	// Counter reset: two more failures should not yet open (needs 3 consecutive).
	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("x") }))
	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("x") }))
	assert.Equal(t, StateClosed, cb.State())
}

func TestBreaker_HalfOpenLimitsProbes(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 1, Cooldown: time.Second, HalfOpenMax: 1})
	cb.now = func() time.Time { return now }

	require.Error(t, cb.Execute(context.Background(), func(ctx context.Context) error { return errors.New("x") }))
	now = now.Add(2 * time.Second)

	// First goroutine takes the single probe slot and blocks inside fn; the
	// second must be rejected with ErrOpen.
	release := make(chan struct{})
	probeStarted := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cb.Execute(context.Background(), func(ctx context.Context) error {
			close(probeStarted)
			<-release
			return nil
		})
	}()

	<-probeStarted
	err := cb.Execute(context.Background(), func(ctx context.Context) error { return nil })
	assert.ErrorIs(t, err, ErrOpen, "half-open must reject probes beyond HalfOpenMax")
	close(release)
	wg.Wait()
}

func TestBreaker_ConcurrentExecuteRaceClean(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreaker(BreakerConfig{FailureThreshold: 5, Cooldown: time.Millisecond, HalfOpenMax: 2})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = cb.Execute(context.Background(), func(ctx context.Context) error {
				if n%2 == 0 {
					return errors.New("x")
				}
				return nil
			})
			_ = cb.State()
		}(i)
	}
	wg.Wait()
}

func TestBreaker_DefaultsApplied(t *testing.T) {
	t.Parallel()
	// Zero-value config must be usable via constructor defaults.
	cb := NewCircuitBreaker(BreakerConfig{})
	assert.Equal(t, StateClosed, cb.State())
	require.NoError(t, cb.Execute(context.Background(), func(ctx context.Context) error { return nil }))
}
