// Package adapters holds the item domain's persistence and messaging
// adapters: an in-memory Database (the default Storer), a read-through Cache
// decorator, and a Publisher bridging to internal/messaging. They implement
// item's ports and depend on item, never the reverse, so the domain package
// stays free of infrastructure.
package adapters

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/luisfelipecoelho/go-template/internal/item"
)

// Database is a goroutine-safe in-memory item.Storer — the zero-infrastructure
// default. Swap it for a real database (e.g. Postgres with parameterized
// queries only); the service depends on item.Storer, not this type.
//
// The zero value is unusable: items is a nil map, which panics on first write.
// Construct via NewDatabase.
type Database struct {
	mu    sync.RWMutex
	items map[string]item.Item
}

var _ item.Storer = (*Database)(nil)

// NewDatabase returns an empty in-memory store.
func NewDatabase() *Database {
	return &Database{items: make(map[string]item.Item)}
}

// checkCtx returns a wrapped error if ctx is done, naming the aborted verb.
func checkCtx(ctx context.Context, verb string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	return nil
}

// Create stores a new item; the id must not already exist.
func (d *Database) Create(ctx context.Context, itm item.Item) error {
	if err := checkCtx(ctx, "creating item"); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.items[itm.ID]; exists {
		return fmt.Errorf("item id %s already exists", itm.ID)
	}
	d.items[itm.ID] = itm
	return nil
}

// QueryByID returns a copy of the stored item.
func (d *Database) QueryByID(ctx context.Context, id string) (item.Item, error) {
	if err := checkCtx(ctx, "querying item"); err != nil {
		return item.Item{}, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	itm, ok := d.items[id]
	if !ok {
		return item.Item{}, fmt.Errorf("id %s: %w", id, item.ErrNotFound)
	}
	return itm, nil
}

// Query returns one page of items ordered by CreatedAt then ID, plus the total
// count. Pages beyond the range return an empty slice.
func (d *Database) Query(ctx context.Context, page item.Page) ([]item.Item, int, error) {
	if err := checkCtx(ctx, "querying items"); err != nil {
		return nil, 0, err
	}

	d.mu.RLock()
	all := make([]item.Item, 0, len(d.items))
	for _, itm := range d.items {
		all = append(all, itm)
	}
	d.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID < all[j].ID
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt)
	})

	total := len(all)
	start := (page.Number - 1) * page.Rows
	if start >= total {
		return []item.Item{}, total, nil
	}
	end := min(start+page.Rows, total)
	return all[start:end], total, nil
}

// Update replaces the stored item; it must exist.
func (d *Database) Update(ctx context.Context, itm item.Item) error {
	if err := checkCtx(ctx, "updating item"); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.items[itm.ID]; !ok {
		return fmt.Errorf("id %s: %w", itm.ID, item.ErrNotFound)
	}
	d.items[itm.ID] = itm
	return nil
}

// Delete removes the stored item; it must exist.
func (d *Database) Delete(ctx context.Context, id string) error {
	if err := checkCtx(ctx, "deleting item"); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.items[id]; !ok {
		return fmt.Errorf("id %s: %w", id, item.ErrNotFound)
	}
	delete(d.items, id)
	return nil
}
