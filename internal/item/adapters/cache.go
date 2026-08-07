// cache.go — a read-through cache decorator over an item.Storer. It fronts the
// real store with an internal/cache backend (Redis/in-memory), so a service
// runs its database and a Redis cache at the same time: reads hit the cache
// and populate on a miss; writes keep it warm and deletes evict. The cache is
// best-effort — a backend failure is logged at error level (ADR-6) and
// downgraded to a miss, never failing the operation, because the decorated
// store is the source of truth.
package adapters

import (
	"context"
	jsonv2 "encoding/json/v2"
	"log/slog"
	"time"

	"github.com/luisfelipecoelho/go-template/internal/cache"
	"github.com/luisfelipecoelho/go-template/internal/item"
)

// keyPrefix namespaces item entries so one cache can back several domains.
const keyPrefix = "item:"

// CacheStore is an item.Storer that fronts another item.Storer with a cache.
type CacheStore struct {
	next  item.Storer
	cache cache.Cache
	ttl   time.Duration
	log   *slog.Logger
}

var _ item.Storer = (*CacheStore)(nil)

// NewCache wraps next with c. ttl bounds cached entries (non-positive means no
// expiry). Only single-item reads are cached; list queries always hit next
// because page invalidation is not tractable.
func NewCache(next item.Storer, c cache.Cache, ttl time.Duration, log *slog.Logger) *CacheStore {
	return &CacheStore{next: next, cache: c, ttl: ttl, log: log}
}

// Create writes through next, then warms the cache with the new item.
func (s *CacheStore) Create(ctx context.Context, itm item.Item) error {
	if err := s.next.Create(ctx, itm); err != nil {
		return err
	}
	s.warm(ctx, itm)
	return nil
}

// QueryByID serves from cache on a hit, otherwise reads next and populates the
// cache. Cache faults degrade to a direct read.
func (s *CacheStore) QueryByID(ctx context.Context, id string) (item.Item, error) {
	if itm, ok := s.fromCache(ctx, id); ok {
		return itm, nil
	}
	itm, err := s.next.QueryByID(ctx, id)
	if err != nil {
		return item.Item{}, err
	}
	s.warm(ctx, itm)
	return itm, nil
}

// Query is not cached; it delegates straight to next.
func (s *CacheStore) Query(ctx context.Context, page item.Page) ([]item.Item, int, error) {
	return s.next.Query(ctx, page)
}

// Update writes through next, then refreshes the cached copy.
func (s *CacheStore) Update(ctx context.Context, itm item.Item) error {
	if err := s.next.Update(ctx, itm); err != nil {
		return err
	}
	s.warm(ctx, itm)
	return nil
}

// Delete removes from next, then evicts the cached copy.
func (s *CacheStore) Delete(ctx context.Context, id string) error {
	if err := s.next.Delete(ctx, id); err != nil {
		return err
	}
	if err := s.cache.Delete(ctx, keyPrefix+id); err != nil {
		s.logFail(ctx, "cache evict failed", err)
	}
	return nil
}

// logFail logs a best-effort cache failure at error level (ADR-6).
func (s *CacheStore) logFail(ctx context.Context, msg string, err error) {
	s.log.ErrorContext(ctx, msg, slog.String("error", err.Error()))
}

// fromCache returns the cached item for id, or ok=false on a miss, a decode
// failure, or a backend error (all logged and treated as a miss).
func (s *CacheStore) fromCache(ctx context.Context, id string) (item.Item, bool) {
	data, ok, err := s.cache.Get(ctx, keyPrefix+id)
	if err != nil {
		s.logFail(ctx, "cache read failed", err)
		return item.Item{}, false
	}
	if !ok {
		return item.Item{}, false
	}
	var itm item.Item
	if err := jsonv2.Unmarshal(data, &itm); err != nil {
		s.logFail(ctx, "cache decode failed", err)
		return item.Item{}, false
	}
	return itm, true
}

// warm stores itm in the cache best-effort.
func (s *CacheStore) warm(ctx context.Context, itm item.Item) {
	data, err := jsonv2.Marshal(itm)
	if err != nil {
		s.logFail(ctx, "cache marshal failed", err)
		return
	}
	if err := s.cache.Set(ctx, keyPrefix+itm.ID, data, s.ttl); err != nil {
		s.logFail(ctx, "cache write failed", err)
	}
}
