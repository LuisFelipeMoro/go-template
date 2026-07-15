// Package item is the reference domain: entity, invariants, and business
// operations. It owns its dependency contracts in ports.go (Storer,
// EventPublisher) so storage and messaging implementations can be swapped
// without touching this package — new domains should copy this shape.
package item

import (
	"fmt"
	"strings"
	"time"
)

// Business invariants for Item fields.
const (
	maxNameLen = 120
	maxRows    = 100
)

// Item is the domain entity. Money is integer cents — never floats.
type Item struct {
	ID         string // UUID v4
	Name       string // 1..120 chars after trimming
	Quantity   int    // >= 0
	PriceCents int64  // >= 0
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewItem carries the fields required to create an Item.
type NewItem struct {
	Name       string
	Quantity   int
	PriceCents int64
}

// UpdateItem carries a partial update; nil fields are left untouched. At least
// one field must be set.
type UpdateItem struct {
	Name       *string
	Quantity   *int
	PriceCents *int64
}

// Page is 1-based offset pagination, bounded to maxRows per page.
type Page struct {
	Number int
	Rows   int
}

func (ni NewItem) validate() error {
	name := strings.TrimSpace(ni.Name)
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("name must be 1..%d chars: %w", maxNameLen, ErrInvalidArgument)
	}
	if ni.Quantity < 0 {
		return fmt.Errorf("quantity must be >= 0: %w", ErrInvalidArgument)
	}
	if ni.PriceCents < 0 {
		return fmt.Errorf("price_cents must be >= 0: %w", ErrInvalidArgument)
	}
	return nil
}

func (ui UpdateItem) validate() error {
	if ui.Name == nil && ui.Quantity == nil && ui.PriceCents == nil {
		return fmt.Errorf("update requires at least one field: %w", ErrInvalidArgument)
	}
	if ui.Name != nil {
		name := strings.TrimSpace(*ui.Name)
		if name == "" || len(name) > maxNameLen {
			return fmt.Errorf("name must be 1..%d chars: %w", maxNameLen, ErrInvalidArgument)
		}
	}
	if ui.Quantity != nil && *ui.Quantity < 0 {
		return fmt.Errorf("quantity must be >= 0: %w", ErrInvalidArgument)
	}
	if ui.PriceCents != nil && *ui.PriceCents < 0 {
		return fmt.Errorf("price_cents must be >= 0: %w", ErrInvalidArgument)
	}
	return nil
}

func (p Page) validate() error {
	if p.Number < 1 {
		return fmt.Errorf("page must be >= 1: %w", ErrInvalidArgument)
	}
	if p.Rows < 1 || p.Rows > maxRows {
		return fmt.Errorf("rows must be 1..%d: %w", maxRows, ErrInvalidArgument)
	}
	return nil
}
