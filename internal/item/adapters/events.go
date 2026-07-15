// events.go — the item domain's messaging adapter: it bridges the domain's
// item.EventPublisher port onto the transport-neutral internal/messaging.
// Swapping brokers means swapping the messaging.Publisher passed in here; the
// domain never changes.
package adapters

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"

	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/messaging"
)

// Publisher marshals domain events into messages on a fixed topic.
type Publisher struct {
	pub   messaging.Publisher
	topic string
}

var _ item.EventPublisher = (*Publisher)(nil)

// NewPublisher returns an adapter publishing item events to topic.
func NewPublisher(pub messaging.Publisher, topic string) *Publisher {
	return &Publisher{pub: pub, topic: topic}
}

// Publish serializes evt as JSON, keyed by item id for partition affinity.
func (p *Publisher) Publish(ctx context.Context, evt item.Event) error {
	payload, err := jsonv2.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshaling event %s: %w", evt.Type, err)
	}

	msg := messaging.Message{Topic: p.topic, Key: evt.ItemID, Payload: payload}
	if err := p.pub.Publish(ctx, msg); err != nil {
		return fmt.Errorf("publishing event %s: %w", evt.Type, err)
	}
	return nil
}
