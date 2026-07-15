// service.go
package item

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/luisfelipecoelho/go-template/pkg/uid"
)

// Service implements the item business operations. It is stateless; all
// dependencies arrive via NewService and are used through the interfaces this
// package owns.
type Service struct {
	log   *slog.Logger
	store Storer
	pub   EventPublisher
}

// NewService constructs the item Service. All dependencies are required.
func NewService(log *slog.Logger, store Storer, pub EventPublisher) *Service {
	return &Service{log: log, store: store, pub: pub}
}

// Create validates ni, persists a new Item, and publishes item.created.
func (s *Service) Create(ctx context.Context, ni NewItem) (Item, error) {
	if err := ni.validate(); err != nil {
		return Item{}, err
	}

	id, err := uid.New()
	if err != nil {
		return Item{}, fmt.Errorf("generating item id: %w", err)
	}

	now := time.Now().UTC()
	itm := Item{
		ID:         id,
		Name:       ni.Name,
		Quantity:   ni.Quantity,
		PriceCents: ni.PriceCents,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.store.Create(ctx, itm); err != nil {
		return Item{}, fmt.Errorf("creating item: %w", err)
	}

	s.publish(ctx, Event{Type: EventCreated, ItemID: itm.ID, OccurredAt: now})
	return itm, nil
}

// QueryByID returns the item with the given UUID.
func (s *Service) QueryByID(ctx context.Context, id string) (Item, error) {
	if err := validateID(id); err != nil {
		return Item{}, err
	}

	itm, err := s.store.QueryByID(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("querying item %s: %w", id, err)
	}
	return itm, nil
}

// Query returns one page of items plus the total count.
func (s *Service) Query(ctx context.Context, page Page) ([]Item, int, error) {
	if err := page.validate(); err != nil {
		return nil, 0, err
	}

	items, total, err := s.store.Query(ctx, page)
	if err != nil {
		return nil, 0, fmt.Errorf("querying items: %w", err)
	}
	return items, total, nil
}

// Update applies the non-nil fields of ui to the stored item and publishes
// item.updated.
func (s *Service) Update(ctx context.Context, id string, ui UpdateItem) (Item, error) {
	if err := validateID(id); err != nil {
		return Item{}, err
	}
	if err := ui.validate(); err != nil {
		return Item{}, err
	}

	itm, err := s.store.QueryByID(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("querying item %s for update: %w", id, err)
	}

	if ui.Name != nil {
		itm.Name = *ui.Name
	}
	if ui.Quantity != nil {
		itm.Quantity = *ui.Quantity
	}
	if ui.PriceCents != nil {
		itm.PriceCents = *ui.PriceCents
	}
	itm.UpdatedAt = time.Now().UTC()

	if err := s.store.Update(ctx, itm); err != nil {
		return Item{}, fmt.Errorf("updating item %s: %w", id, err)
	}

	s.publish(ctx, Event{Type: EventUpdated, ItemID: itm.ID, OccurredAt: itm.UpdatedAt})
	return itm, nil
}

// Delete removes the item and publishes item.deleted.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}

	if err := s.store.Delete(ctx, id); err != nil {
		return fmt.Errorf("deleting item %s: %w", id, err)
	}

	s.publish(ctx, Event{Type: EventDeleted, ItemID: id, OccurredAt: time.Now().UTC()})
	return nil
}

// publish emits the event best-effort: the state change already committed, so
// failing the operation here would lie to the caller. The failure is logged at
// error level (the only log this package emits — ADR-6). For exactly-once
// delivery, replace this with a transactional outbox once a real database
// backs Storer.
func (s *Service) publish(ctx context.Context, evt Event) {
	if err := s.pub.Publish(ctx, evt); err != nil {
		s.log.ErrorContext(ctx, "publishing domain event",
			slog.String("event_type", evt.Type),
			slog.String("item_id", evt.ItemID),
			slog.String("error", err.Error()),
		)
	}
}

func validateID(id string) error {
	if err := uid.Validate(id); err != nil {
		return fmt.Errorf("id must be a UUID: %w", ErrInvalidArgument)
	}
	return nil
}
