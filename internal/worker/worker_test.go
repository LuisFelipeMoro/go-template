// worker_test.go
package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/messaging"
)

// fakeConsumer feeds a fixed set of messages then blocks until ctx is done.
type fakeConsumer struct {
	msgs []messaging.Message
}

func (f *fakeConsumer) Consume(ctx context.Context, topic string, h messaging.Handler) error {
	for _, m := range f.msgs {
		if ctx.Err() != nil {
			return nil
		}
		_ = h(ctx, m)
	}
	<-ctx.Done()
	return nil
}

// countingMetrics records processed/failed counts.
type countingMetrics struct {
	mu        sync.Mutex
	processed int
	failed    int
}

func (m *countingMetrics) MessageProcessed(context.Context) {
	m.mu.Lock()
	m.processed++
	m.mu.Unlock()
}
func (m *countingMetrics) MessageFailed(context.Context) { m.mu.Lock(); m.failed++; m.mu.Unlock() }

func newTestWorker(t *testing.T, consumer messaging.Consumer, m Metrics, h Handler, buf *bytes.Buffer) *Worker {
	t.Helper()
	if buf == nil {
		buf = &bytes.Buffer{}
	}
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(log, consumer, "items", 500*time.Millisecond, m, h)
}

func runWorker(t *testing.T, w *Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	// Give the consumer time to drain its fixed messages.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "clean drain returns nil")
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not drain on cancel")
	}
}

func TestWorker_ProcessesSuccessfully_NoLogs(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	m := &countingMetrics{}
	consumer := &fakeConsumer{msgs: []messaging.Message{{Topic: "items"}, {Topic: "items"}}}
	w := newTestWorker(t, consumer, m, func(ctx context.Context, msg messaging.Message) error { return nil }, &buf)

	runWorker(t, w)

	assert.Equal(t, 2, m.processed)
	assert.Equal(t, 0, m.failed)
	assert.Empty(t, buf.String(), "ADR-6: successful processing emits no logs")
}

func TestWorker_HandlerError_LogsAndCounts(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	m := &countingMetrics{}
	consumer := &fakeConsumer{msgs: []messaging.Message{{Topic: "items"}}}
	w := newTestWorker(t, consumer, m, func(ctx context.Context, msg messaging.Message) error {
		return errors.New("processing failed")
	}, &buf)

	runWorker(t, w)

	assert.Equal(t, 0, m.processed)
	assert.Equal(t, 1, m.failed)
	assert.Contains(t, buf.String(), `"level":"ERROR"`)
}

func TestWorker_HandlerPanic_RecoversAndContinues(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	m := &countingMetrics{}
	consumer := &fakeConsumer{msgs: []messaging.Message{{Topic: "items", Key: "boom"}, {Topic: "items", Key: "ok"}}}

	var mu sync.Mutex
	var handled []string
	w := newTestWorker(t, consumer, m, func(ctx context.Context, msg messaging.Message) error {
		if msg.Key == "boom" {
			panic("handler exploded")
		}
		mu.Lock()
		handled = append(handled, msg.Key)
		mu.Unlock()
		return nil
	}, &buf)

	runWorker(t, w)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, handled, "ok", "loop must continue past a panicking message")
	assert.Equal(t, 1, m.failed, "panic counts as a failure")
	assert.Contains(t, buf.String(), `"level":"ERROR"`)
}

func TestWorker_PerMessageTimeout(t *testing.T) {
	t.Parallel()
	m := &countingMetrics{}
	consumer := &fakeConsumer{msgs: []messaging.Message{{Topic: "items"}}}
	// Handler waits for its context to be canceled by the per-message timeout.
	var deadlineHit bool
	var mu sync.Mutex
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
	handler := func(ctx context.Context, msg messaging.Message) error {
		<-ctx.Done()
		mu.Lock()
		deadlineHit = true
		mu.Unlock()
		return ctx.Err()
	}
	w := New(log, consumer, "items", 20*time.Millisecond, m, handler)

	runWorker(t, w)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, deadlineHit, "per-message context must be canceled by the timeout")
}

func TestWorker_InjectedHandlerProcessesMessage(t *testing.T) {
	t.Parallel()
	// The injected handler receives each message and a success is counted.
	m := &countingMetrics{}
	consumer := &fakeConsumer{msgs: []messaging.Message{{Topic: "items", Payload: []byte(`{}`)}}}
	var got int
	w := newTestWorker(t, consumer, m, func(context.Context, messaging.Message) error {
		got++
		return nil
	}, nil)

	runWorker(t, w)
	assert.Equal(t, 1, got, "injected handler ran once")
	assert.Equal(t, 1, m.processed)
}
