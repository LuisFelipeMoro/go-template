// runner_test.go
package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardLogger returns a logger that drops output; tests assert behaviour, not logs.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// recorder captures the interleaved start/stop order across components under a mutex.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// blockingComp returns a Component that blocks in Start until ctx is done, then
// records its stop. started is closed once Start has begun.
func blockingComp(rec *recorder, name string) (Component, chan struct{}) {
	started := make(chan struct{})
	var once sync.Once
	return Component{
		Name: name,
		Start: func(ctx context.Context) error {
			once.Do(func() { close(started) })
			rec.add("start:" + name)
			<-ctx.Done()
			return nil
		},
		Stop: func(ctx context.Context) error {
			rec.add("stop:" + name)
			return nil
		},
	}, started
}

func TestRunner_StartErrorCancelsOthersAndIsReturned(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	sentinel := errors.New("boom")

	r := NewRunner(discardLogger(), time.Second)
	// A long-running component that should be cancelled by the failing one.
	blocker, started := blockingComp(rec, "blocker")
	r.Add(blocker)
	r.Add(Component{
		Name: "failer",
		Start: func(ctx context.Context) error {
			<-started // ensure blocker is running before we fail
			return sentinel
		},
		Stop: func(ctx context.Context) error {
			rec.add("stop:failer")
			return nil
		},
	})

	err := r.Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "fatal start error must be wrapped and returned")
	assert.Contains(t, rec.snapshot(), "stop:blocker", "other components must be stopped")
}

func TestRunner_StopsInReverseOrderOnCancel(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	r := NewRunner(discardLogger(), time.Second)

	var starts []chan struct{}
	for _, name := range []string{"a", "b", "c"} {
		c, started := blockingComp(rec, name)
		r.Add(c)
		starts = append(starts, started)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	for _, s := range starts {
		<-s // all started
	}
	cancel() // simulate SIGTERM
	require.NoError(t, <-done)

	// Extract stop order only.
	var stops []string
	for _, c := range rec.snapshot() {
		if len(c) > 5 && c[:5] == "stop:" {
			stops = append(stops, c[5:])
		}
	}
	assert.Equal(t, []string{"c", "b", "a"}, stops, "stop must run in reverse registration order")
}

func TestRunner_StopExceedingTimeoutReturnsError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	r := NewRunner(discardLogger(), 20*time.Millisecond)

	c, started := blockingComp(rec, "slow")
	c.Stop = func(ctx context.Context) error {
		<-ctx.Done() // never finishes before the shutdown timeout fires
		return ctx.Err()
	}
	r.Add(c)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	<-started
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded, "timed-out stop must surface deadline error")
	case <-time.After(2 * time.Second):
		t.Fatal("Run hung past shutdown timeout")
	}
}

func TestRunner_JoinsStopErrorsWhenNoStartError(t *testing.T) {
	t.Parallel()
	err1 := errors.New("stop-a-failed")
	err2 := errors.New("stop-b-failed")
	r := NewRunner(discardLogger(), time.Second)

	mk := func(name string, stopErr error) Component {
		started := make(chan struct{})
		var once sync.Once
		return Component{
			Name: name,
			Start: func(ctx context.Context) error {
				once.Do(func() { close(started) })
				<-ctx.Done()
				return nil
			},
			Stop: func(ctx context.Context) error { return stopErr },
		}
	}
	r.Add(mk("a", err1))
	r.Add(mk("b", err2))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()

	err := <-done
	require.Error(t, err)
	assert.ErrorIs(t, err, err1)
	assert.ErrorIs(t, err, err2)
}

func TestRunner_NilSafeWithZeroComponents(t *testing.T) {
	t.Parallel()
	r := NewRunner(discardLogger(), time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	cancel()
	assert.NoError(t, <-done)
}

func TestRunner_NilStopIsSkipped(t *testing.T) {
	t.Parallel()
	r := NewRunner(discardLogger(), time.Second)
	started := make(chan struct{})
	var once sync.Once
	r.Add(Component{
		Name: "no-stop",
		Start: func(ctx context.Context) error {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return nil
		},
		Stop: nil, // must not panic
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	<-started
	cancel()
	assert.NoError(t, <-done)
}

func TestRunner_SetupStyleStartReturningNilImmediately(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	r := NewRunner(discardLogger(), time.Second)

	r.Add(Component{
		Name:  "setup",
		Start: func(ctx context.Context) error { rec.add("start:setup"); return nil },
		Stop:  func(ctx context.Context) error { rec.add("stop:setup"); return nil },
	})
	// A blocker keeps Run alive until cancel; otherwise setup returning nil would end the group.
	blocker, started := blockingComp(rec, "blocker")
	r.Add(blocker)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	<-started
	cancel()
	require.NoError(t, <-done)
	assert.Contains(t, rec.snapshot(), "stop:setup")
}
