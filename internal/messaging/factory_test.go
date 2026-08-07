// factory_test.go
package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_MemoryDriver(t *testing.T) {
	t.Parallel()
	pub, con, closer, err := New("memory")
	require.NoError(t, err)
	require.NotNil(t, pub)
	require.NotNil(t, con)
	require.NotNil(t, closer)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	// Round-trip proves it is the real Memory bus.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Message, 1)
	go func() {
		assert.NoError(t, con.Consume(ctx, "t", func(_ context.Context, m Message) error { got <- m; return nil }))
	}()
	require.NoError(t, pub.Publish(context.Background(), Message{Topic: "t", Key: "k"}))
	select {
	case m := <-got:
		assert.Equal(t, "k", m.Key)
	case <-time.After(time.Second):
		t.Fatal("memory bus did not deliver")
	}
}

func TestNew_NoneDriverDetachesMessaging(t *testing.T) {
	t.Parallel()
	pub, con, closer, err := New("none")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	// Publish is a silent no-op.
	require.NoError(t, pub.Publish(context.Background(), Message{Topic: "t"}))

	// Consume blocks until ctx cancel and delivers nothing.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	delivered := false
	go func() {
		done <- con.Consume(ctx, "t", func(context.Context, Message) error { delivered = true; return nil })
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("discard consumer did not return on cancel")
	}
	assert.False(t, delivered, "none driver must deliver no messages")
}

func TestNew_UnknownDriverErrors(t *testing.T) {
	t.Parallel()
	_, _, _, err := New("kafka")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kafka")
}

func TestDiscard_ZeroValueUsable(t *testing.T) {
	t.Parallel()
	var d Discard // zero value, no constructor
	require.NoError(t, d.Publish(context.Background(), Message{}))
}
