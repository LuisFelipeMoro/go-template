// memory.go
package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// defaultBufferSize bounds each topic's in-flight messages; a full buffer makes
// Publish fail loudly rather than block or drop silently.
const defaultBufferSize = 256

// ErrClosed reports an operation on a closed bus.
var ErrClosed = errors.New("bus closed")

// Memory is an in-process Publisher + Consumer for local development and
// tests. One queue per topic; multiple consumers on the same topic compete for
// messages (like a shared queue, not fan-out).
//
// The zero value is unusable: topics is a nil map and closed is a nil channel,
// so a publish would panic and a close would block forever. Construct via
// NewMemory.
type Memory struct {
	mu     sync.Mutex
	topics map[string]chan Message
	closed chan struct{}
	once   sync.Once
}

// Compile-time interface proofs.
var (
	_ Publisher = (*Memory)(nil)
	_ Consumer  = (*Memory)(nil)
)

// NewMemory returns an open in-memory bus.
func NewMemory() *Memory {
	return &Memory{
		topics: make(map[string]chan Message),
		closed: make(chan struct{}),
	}
}

// topic returns (lazily creating) the channel for name.
func (m *Memory) topic(name string) chan Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.topics[name]
	if !ok {
		ch = make(chan Message, defaultBufferSize)
		m.topics[name] = ch
	}
	return ch
}

// Publish enqueues msg on its topic. It fails when the bus is closed, the
// buffer is full, or ctx is done.
func (m *Memory) Publish(ctx context.Context, msg Message) error {
	select {
	case <-m.closed:
		return fmt.Errorf("publishing to %s: %w", msg.Topic, ErrClosed)
	default:
	}

	select {
	case m.topic(msg.Topic) <- msg:
		return nil
	case <-m.closed:
		return fmt.Errorf("publishing to %s: %w", msg.Topic, ErrClosed)
	case <-ctx.Done():
		return fmt.Errorf("publishing to %s: %w", msg.Topic, ctx.Err())
	default:
		return fmt.Errorf("publishing to %s: buffer full (%d)", msg.Topic, defaultBufferSize)
	}
}

// Consume delivers topic messages to h until ctx is done or the bus closes —
// both return nil (clean shutdown). Handler errors do not stop the loop; the
// handler owns its failure accounting.
func (m *Memory) Consume(ctx context.Context, topic string, h Handler) error {
	ch := m.topic(topic)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.closed:
			return nil
		case msg := <-ch:
			if err := h(ctx, msg); err != nil {
				// Deliberate no-op: the in-memory bus has no dead-letter or
				// redelivery, and the consumer (the worker) already logs and
				// counts its own failures before returning. A real broker
				// adapter is where nack/redelivery belongs.
				continue
			}
		}
	}
}

// Close shuts the bus down: publishes fail and consumers unblock. Idempotent.
func (m *Memory) Close() error {
	m.once.Do(func() { close(m.closed) })
	return nil
}
