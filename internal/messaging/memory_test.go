// memory_test.go
package messaging

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemory_PublishDeliveredInOrder(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	t.Cleanup(func() { _ = m.Close() })

	var mu sync.Mutex
	var got []Message
	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer close(done)
		_ = m.Consume(ctx, "items", func(ctx context.Context, msg Message) error {
			mu.Lock()
			got = append(got, msg)
			mu.Unlock()
			return nil
		})
	}()

	for i := 0; i < 3; i++ {
		require.NoError(t, m.Publish(context.Background(), Message{
			Topic: "items", Key: fmt.Sprintf("k%d", i), Payload: []byte{byte(i)},
		}))
	}

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 3
	}, 2*time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for i, msg := range got {
		assert.Equal(t, fmt.Sprintf("k%d", i), msg.Key, "order and key preserved")
		assert.Equal(t, "items", msg.Topic)
	}
	cancel()
	<-done
}

func TestMemory_CloseUnblocksConsumerAndRejectsPublish(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	done := make(chan error, 1)
	go func() {
		done <- m.Consume(context.Background(), "items", func(context.Context, Message) error { return nil })
	}()

	time.Sleep(20 * time.Millisecond) // let the consumer subscribe
	require.NoError(t, m.Close())

	select {
	case err := <-done:
		assert.NoError(t, err, "Close must unblock Consume cleanly")
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return after Close")
	}

	assert.Error(t, m.Publish(context.Background(), Message{Topic: "items"}), "publish after Close must error")
	assert.NoError(t, m.Close(), "double Close must be safe")
}

func TestMemory_ConsumeReturnsNilOnCtxCancel(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	t.Cleanup(func() { _ = m.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.Consume(ctx, "items", func(context.Context, Message) error { return nil })
	}()
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err, "ctx cancel is a clean shutdown")
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return on ctx cancel")
	}
}

func TestMemory_FullBufferPublishErrors(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	t.Cleanup(func() { _ = m.Close() })

	// No consumer: fill the topic buffer to capacity, next publish must fail loudly.
	var err error
	for i := 0; i < defaultBufferSize+1; i++ {
		err = m.Publish(context.Background(), Message{Topic: "flood", Payload: []byte{1}})
		if err != nil {
			break
		}
	}
	assert.Error(t, err, "publish into a full buffer must error, not drop or block")
}

func TestMemory_HandlerErrorDoesNotStopLoop(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	t.Cleanup(func() { _ = m.Close() })

	var mu sync.Mutex
	var seen int
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = m.Consume(ctx, "items", func(ctx context.Context, msg Message) error {
			mu.Lock()
			seen++
			mu.Unlock()
			return fmt.Errorf("handler failure")
		})
	}()

	require.NoError(t, m.Publish(context.Background(), Message{Topic: "items"}))
	require.NoError(t, m.Publish(context.Background(), Message{Topic: "items"}))

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen == 2
	}, 2*time.Second, 10*time.Millisecond, "loop must continue past handler errors")
}

func TestMemory_ConcurrentPublishersRaceClean(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	t.Cleanup(func() { _ = m.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	count := 0
	go func() {
		_ = m.Consume(ctx, "items", func(context.Context, Message) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		})
	}()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = m.Publish(context.Background(), Message{Topic: "items"})
			}
		}()
	}
	wg.Wait()

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return count == 100
	}, 2*time.Second, 10*time.Millisecond)
}
