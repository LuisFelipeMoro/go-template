// Package messaging defines broker-agnostic messaging contracts plus an
// in-memory implementation. Real brokers (SQS, SNS, Kafka, RabbitMQ) are
// integrated by implementing Publisher and Consumer here or in a sub-package —
// no broker SDK type may appear in these interfaces, so swapping brokers never
// touches domains, adapters, or workers.
//
// The template deliberately ships no real broker: the interfaces plus the
// driver switch in New are the product. See the package README for the
// three-step recipe to plug one in.
package messaging

import "context"

// Message is the transport-neutral unit of communication. Payload is opaque
// bytes; Key enables partition/ordering affinity on brokers that support it.
type Message struct {
	Topic   string
	Key     string
	Payload []byte
}

// Handler processes one message. Returning an error signals a failed delivery;
// redelivery/DLQ semantics belong to the broker implementation.
type Handler func(ctx context.Context, msg Message) error

// Publisher sends messages to a topic.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// Consumer blocks delivering topic messages to h until ctx is done or the
// underlying transport closes; both are clean shutdowns returning nil.
type Consumer interface {
	Consume(ctx context.Context, topic string, h Handler) error
}
