package item

import (
	"errors"
	"strings"
	"testing"
)

// The validators are the domain's only defence against whatever survives the
// HTTP layer, so their contract must hold for every input rather than for the
// dozen a table can name. Two invariants are fuzzed:
//
//  1. a rejection is ALWAYS ErrInvalidArgument. The HTTP adapter maps on that
//     sentinel with errors.Is; anything else collapses to a generic 500, which
//     turns a caller's bad request into a page for the on-call.
//  2. acceptance really does imply the documented bounds. A validator that
//     accepts an over-long name is worse than none, because every layer below
//     it has stopped checking.
func FuzzNewItemValidate(f *testing.F) {
	f.Add("widget", 1, int64(100))
	f.Add("", 0, int64(0))
	f.Add("   ", 0, int64(0))
	f.Add(strings.Repeat("a", maxNameLen), 0, int64(0))
	f.Add(strings.Repeat("a", maxNameLen+1), 0, int64(0))
	f.Add("widget", -1, int64(0))
	f.Add("widget", 0, int64(-1))
	f.Add("\x00", 0, int64(0))
	f.Add("日本語", 0, int64(0))

	f.Fuzz(func(t *testing.T, name string, qty int, price int64) {
		err := NewItem{Name: name, Quantity: qty, PriceCents: price}.validate()

		if err != nil {
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("rejection did not wrap ErrInvalidArgument: %v", err)
			}
			return
		}

		trimmed := strings.TrimSpace(name)
		switch {
		case trimmed == "":
			t.Fatalf("accepted a blank name: %q", name)
		case len(trimmed) > maxNameLen:
			t.Fatalf("accepted a %d-byte name, max is %d", len(trimmed), maxNameLen)
		case qty < 0:
			t.Fatalf("accepted a negative quantity: %d", qty)
		case price < 0:
			t.Fatalf("accepted a negative price: %d", price)
		}
	})
}

// UpdateItem's extra rule is that an all-nil update must be refused: applying it
// would bump UpdatedAt and publish an item.updated event describing no change,
// which is a lie every downstream consumer would act on.
func FuzzUpdateItemValidate(f *testing.F) {
	f.Add(false, "", false, 0, false, int64(0))
	f.Add(true, "widget", true, 1, true, int64(100))
	f.Add(true, "", false, 0, false, int64(0))
	f.Add(true, strings.Repeat("a", maxNameLen+1), false, 0, false, int64(0))
	f.Add(false, "", true, -1, false, int64(0))
	f.Add(false, "", false, 0, true, int64(-1))

	f.Fuzz(func(t *testing.T, hasName bool, name string, hasQty bool, qty int, hasPrice bool, price int64) {
		var ui UpdateItem
		if hasName {
			ui.Name = &name
		}
		if hasQty {
			ui.Quantity = &qty
		}
		if hasPrice {
			ui.PriceCents = &price
		}

		err := ui.validate()

		if err != nil {
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("rejection did not wrap ErrInvalidArgument: %v", err)
			}
			return
		}

		if !hasName && !hasQty && !hasPrice {
			t.Fatal("accepted an update with no fields set")
		}
		if hasName {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" || len(trimmed) > maxNameLen {
				t.Fatalf("accepted an out-of-bounds name: %q", name)
			}
		}
		if hasQty && qty < 0 {
			t.Fatalf("accepted a negative quantity: %d", qty)
		}
		if hasPrice && price < 0 {
			t.Fatalf("accepted a negative price: %d", price)
		}
	})
}

// Page bounds what a single query can pull back. An unbounded Rows is a
// denial-of-service handed to any caller who asks for it, so acceptance must
// imply the cap for every integer, not just the ones a table tries.
func FuzzPageValidate(f *testing.F) {
	f.Add(1, 20)
	f.Add(0, 20)
	f.Add(-1, 20)
	f.Add(1, 0)
	f.Add(1, maxRows)
	f.Add(1, maxRows+1)
	f.Add(1, -1)

	f.Fuzz(func(t *testing.T, number, rows int) {
		err := Page{Number: number, Rows: rows}.validate()

		if err != nil {
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("rejection did not wrap ErrInvalidArgument: %v", err)
			}
			return
		}

		if number < 1 {
			t.Fatalf("accepted page %d", number)
		}
		if rows < 1 || rows > maxRows {
			t.Fatalf("accepted rows %d, bound is 1..%d", rows, maxRows)
		}
	})
}
