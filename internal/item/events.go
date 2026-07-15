// events.go
package item

import "time"

// Event types published by the item domain.
const (
	EventCreated = "item.created"
	EventUpdated = "item.updated"
	EventDeleted = "item.deleted"
)

// Event is the domain event envelope. It carries identifiers only — never the
// full entity — so consumers fetch current state and payloads stay small.
type Event struct {
	Type       string    `json:"type"`
	ItemID     string    `json:"item_id"`
	OccurredAt time.Time `json:"occurred_at"`
}
