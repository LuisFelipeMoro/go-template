package adapters

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/pkg/uid"
)

// seedDatabase fills a store with n items so query cost can be measured against
// dataset size rather than a single happy-path row.
func seedDatabase(b *testing.B, n int) *Database {
	b.Helper()

	d := NewDatabase()
	ctx := context.Background()
	base := time.Now().UTC()
	for i := range n {
		id, err := uid.New()
		if err != nil {
			b.Fatal(err)
		}
		if err := d.Create(ctx, item.Item{
			ID:         id,
			Name:       fmt.Sprintf("item-%d", i),
			Quantity:   i,
			PriceCents: int64(i),
			CreatedAt:  base.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt:  base,
		}); err != nil {
			b.Fatal(err)
		}
	}
	return d
}

// Query copies and sorts the ENTIRE dataset on every call before slicing out a
// page — O(n log n) per request regardless of page size. That is a deliberate
// trade for an in-memory reference store, but it is also the single most
// important number to see before pointing this at production data: these rows
// are what tell you where "swap it for a real database with an indexed ORDER BY"
// stops being optional.
func BenchmarkDatabase_Query(b *testing.B) {
	ctx := context.Background()
	page := item.Page{Number: 1, Rows: 20}

	for _, n := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			d := seedDatabase(b, n)
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := d.Query(ctx, page); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// QueryByID is the single-item read path, the one the cache decorator fronts.
// It is a map lookup and must stay flat as the dataset grows — if this ever
// tracks n, the cache is hiding a defect rather than saving a round trip.
func BenchmarkDatabase_QueryByID(b *testing.B) {
	ctx := context.Background()

	for _, n := range []int{10, 10000} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			d := seedDatabase(b, n)
			items, _, err := d.Query(ctx, item.Page{Number: 1, Rows: 1})
			if err != nil || len(items) == 0 {
				b.Fatal("seed produced no items")
			}
			id := items[0].ID

			b.ReportAllocs()
			for b.Loop() {
				if _, err := d.QueryByID(ctx, id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
