package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/item"
)

func sample(id string, created time.Time) item.Item {
	return item.Item{ID: id, Name: "widget", Quantity: 1, PriceCents: 100, CreatedAt: created, UpdatedAt: created}
}

func TestDatabase_CreateAndQueryByID(t *testing.T) {
	t.Parallel()
	d := NewDatabase()
	ctx := context.Background()
	itm := sample("a", time.Now())

	require.NoError(t, d.Create(ctx, itm))
	got, err := d.QueryByID(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, itm.Name, got.Name)

	// Duplicate id is rejected.
	require.Error(t, d.Create(ctx, itm))

	// Missing id → ErrNotFound.
	_, err = d.QueryByID(ctx, "missing")
	require.ErrorIs(t, err, item.ErrNotFound)
}

func TestDatabase_UpdateAndDelete(t *testing.T) {
	t.Parallel()
	d := NewDatabase()
	ctx := context.Background()
	require.NoError(t, d.Create(ctx, sample("a", time.Now())))

	upd := sample("a", time.Now())
	upd.Name = "changed"
	require.NoError(t, d.Update(ctx, upd))
	got, _ := d.QueryByID(ctx, "a")
	assert.Equal(t, "changed", got.Name)

	require.NoError(t, d.Delete(ctx, "a"))
	_, err := d.QueryByID(ctx, "a")
	require.ErrorIs(t, err, item.ErrNotFound)

	// Update/Delete of a missing id → ErrNotFound.
	require.ErrorIs(t, d.Update(ctx, upd), item.ErrNotFound)
	require.ErrorIs(t, d.Delete(ctx, "a"), item.ErrNotFound)
}

func TestDatabase_QueryPaginationAndOrdering(t *testing.T) {
	t.Parallel()
	d := NewDatabase()
	ctx := context.Background()
	base := time.Now()
	for i, id := range []string{"c", "a", "b"} {
		require.NoError(t, d.Create(ctx, sample(id, base.Add(time.Duration(i)*time.Second))))
	}

	page, total, err := d.Query(ctx, item.Page{Number: 1, Rows: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, page, 2)
	assert.Equal(t, "c", page[0].ID, "ordered by CreatedAt")

	// Page beyond range → empty slice, full total.
	page, total, err = d.Query(ctx, item.Page{Number: 99, Rows: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Empty(t, page)
}

func TestDatabase_ContextCancellation(t *testing.T) {
	t.Parallel()
	d := NewDatabase()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, d.Create(ctx, sample("a", time.Now())))
	_, err := d.QueryByID(ctx, "a")
	require.Error(t, err)
	_, _, err = d.Query(ctx, item.Page{Number: 1, Rows: 10})
	require.Error(t, err)
	require.Error(t, d.Update(ctx, sample("a", time.Now())))
	require.Error(t, d.Delete(ctx, "a"))
}
