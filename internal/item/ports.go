// ports.go
package item

import "context"

// Storer is owned by this package (the consumer): any storage backend —
// in-memory, Postgres, DynamoDB — satisfies it without the domain changing.
type Storer interface {
	Create(ctx context.Context, itm Item) error
	QueryByID(ctx context.Context, id string) (Item, error)
	Query(ctx context.Context, page Page) ([]Item, int, error)
	Update(ctx context.Context, itm Item) error
	Delete(ctx context.Context, id string) error
}

// EventPublisher is owned by this package: any broker adapter (in-memory bus,
// SQS, SNS, Kafka) satisfies it without the domain changing.
type EventPublisher interface {
	Publish(ctx context.Context, evt Event) error
}
