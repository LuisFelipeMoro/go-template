package adapters

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/messaging"
)

// capturePublisher records the last message and can be forced to fail.
type capturePublisher struct {
	last messaging.Message
	err  error
}

func (c *capturePublisher) Publish(_ context.Context, msg messaging.Message) error {
	if c.err != nil {
		return c.err
	}
	c.last = msg
	return nil
}

func TestPublisher_PublishMarshalsAndKeys(t *testing.T) {
	t.Parallel()
	cap := &capturePublisher{}
	p := NewPublisher(cap, "items")

	evt := item.Event{Type: item.EventCreated, ItemID: "abc"}
	require.NoError(t, p.Publish(context.Background(), evt))

	assert.Equal(t, "items", cap.last.Topic)
	assert.Equal(t, "abc", cap.last.Key, "keyed by item id for partition affinity")

	var got item.Event
	require.NoError(t, jsonv2.Unmarshal(cap.last.Payload, &got))
	assert.Equal(t, evt, got)
}

func TestPublisher_PublishPropagatesError(t *testing.T) {
	t.Parallel()
	p := NewPublisher(&capturePublisher{err: errors.New("broker down")}, "items")
	err := p.Publish(context.Background(), item.Event{Type: item.EventCreated, ItemID: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publishing event")
}
