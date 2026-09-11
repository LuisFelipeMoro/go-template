package auth

import (
	"context"
	"fmt"
	"testing"
)

// Authenticate deliberately compares against EVERY configured key without
// short-circuiting, so its cost is linear in the key count rather than in the
// position of the match. That is the property that makes it timing-safe, and it
// is also the one that makes key-set size a real operational limit — this
// benchmark is how you find out what that limit costs before rotating 200 keys
// into production.
func BenchmarkStaticKeys_Authenticate(b *testing.B) {
	sizes := []int{1, 8, 64}
	for _, n := range sizes {
		keys := make([]string, 0, n)
		for i := range n {
			keys = append(keys, fmt.Sprintf("key-%040d", i))
		}
		sk, err := NewStaticKeys(keys...)
		if err != nil {
			b.Fatal(err)
		}
		ctx := context.Background()

		// The match position must not change the cost; measuring first and last
		// is what would expose a short-circuit creeping back in.
		b.Run(fmt.Sprintf("keys=%d/first", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = sk.Authenticate(ctx, keys[0])
			}
		})
		b.Run(fmt.Sprintf("keys=%d/last", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = sk.Authenticate(ctx, keys[n-1])
			}
		})
		b.Run(fmt.Sprintf("keys=%d/miss", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = sk.Authenticate(ctx, "nope")
			}
		})
	}
}
