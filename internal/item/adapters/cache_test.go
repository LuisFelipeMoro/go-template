package adapters

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/cache"
	"github.com/luisfelipecoelho/go-template/internal/item"
)

// spyDatabase counts QueryByID calls so tests can prove cache hits skip the store.
type spyDatabase struct {
	*Database
	queryHit int
}

func newSpy() *spyDatabase { return &spyDatabase{Database: NewDatabase()} }

func (s *spyDatabase) QueryByID(ctx context.Context, id string) (item.Item, error) {
	s.queryHit++
	return s.Database.QueryByID(ctx, id)
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newCacheStore(t *testing.T) (*CacheStore, *spyDatabase) {
	t.Helper()
	spy := newSpy()
	return NewCache(spy, cache.NewMemory(), time.Minute, discardLog()), spy
}

func cachedSample() item.Item {
	now := time.Now().UTC()
	return item.Item{ID: "11111111-1111-1111-1111-111111111111", Name: "widget", Quantity: 2, PriceCents: 500, CreatedAt: now, UpdatedAt: now}
}

func TestCache_ReadServedFromCacheAfterCreate(t *testing.T) {
	t.Parallel()
	c, spy := newCacheStore(t)
	ctx := context.Background()
	itm := cachedSample()

	require.NoError(t, c.Create(ctx, itm))
	got, err := c.QueryByID(ctx, itm.ID)
	require.NoError(t, err)
	assert.Equal(t, itm.Name, got.Name)
	assert.Zero(t, spy.queryHit, "Create warmed the cache, so the store is never read")
}

func TestCache_MissPopulatesThenServesFromCache(t *testing.T) {
	t.Parallel()
	c, spy := newCacheStore(t)
	ctx := context.Background()
	itm := cachedSample()
	require.NoError(t, spy.Create(ctx, itm)) // seed store directly, bypass cache

	_, err := c.QueryByID(ctx, itm.ID)
	require.NoError(t, err)
	require.Equal(t, 1, spy.queryHit, "first read misses → hits the store")

	_, err = c.QueryByID(ctx, itm.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, spy.queryHit, "second read is a hit → store untouched")
}

func TestCache_UpdateRefreshesAndDeleteEvicts(t *testing.T) {
	t.Parallel()
	c, spy := newCacheStore(t)
	ctx := context.Background()
	itm := cachedSample()
	require.NoError(t, c.Create(ctx, itm))

	itm.Name = "changed"
	require.NoError(t, c.Update(ctx, itm))
	got, err := c.QueryByID(ctx, itm.ID)
	require.NoError(t, err)
	assert.Equal(t, "changed", got.Name)
	assert.Zero(t, spy.queryHit, "served from refreshed cache")

	require.NoError(t, c.Delete(ctx, itm.ID))
	_, err = c.QueryByID(ctx, itm.ID)
	require.ErrorIs(t, err, item.ErrNotFound, "sentinel preserved through the decorator")
	assert.Equal(t, 1, spy.queryHit, "cache evicted → read falls through")
}

func TestCache_QueryBypassesCache(t *testing.T) {
	t.Parallel()
	c, _ := newCacheStore(t)
	_, total, err := c.Query(context.Background(), item.Page{Number: 1, Rows: 10})
	require.NoError(t, err)
	assert.Zero(t, total)
}

// erroringCache fails every Get, to prove reads degrade to the store.
type erroringCache struct{}

func (erroringCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("backend down")
}
func (erroringCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (erroringCache) Delete(context.Context, string) error                     { return nil }

func TestCache_FaultDegradesToStore(t *testing.T) {
	t.Parallel()
	spy := newSpy()
	c := NewCache(spy, erroringCache{}, time.Minute, discardLog())
	ctx := context.Background()
	itm := cachedSample()
	require.NoError(t, spy.Create(ctx, itm))

	got, err := c.QueryByID(ctx, itm.ID)
	require.NoError(t, err, "a cache backend fault must not fail the read")
	assert.Equal(t, itm.Name, got.Name)
	assert.Equal(t, 1, spy.queryHit)
}
