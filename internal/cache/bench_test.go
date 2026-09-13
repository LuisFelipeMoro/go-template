package cache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The in-memory cache sits in front of the store on every single-item read, so
// its cost is charged to every cache hit in the service. Expiry is lazy —
// checked on read — which is what makes the hit path worth measuring separately
// from the expired path: a workload of mostly-expired entries pays the write
// lock on a read, which the hit path does not.
func BenchmarkMemory_Get(b *testing.B) {
	value := []byte(`{"id":"11111111-1111-4111-8111-111111111111","name":"widget"}`)

	tests := []struct {
		name string
		key  string
		seed func(*testing.B, *Memory)
	}{
		{"hit", "k", func(b *testing.B, m *Memory) { seedEntry(b, m, value, time.Hour) }},
		{"miss", "absent", func(*testing.B, *Memory) {}},
		// ttl 0 means no expiry, which skips the deadline comparison on every
		// read — worth separating so the cost of lazy expiry is visible.
		{"no_expiry", "k", func(b *testing.B, m *Memory) { seedEntry(b, m, value, 0) }},
	}
	for _, tc := range tests {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			m := NewMemory()
			tc.seed(b, m)

			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := m.Get(ctx, tc.key); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// seedEntry writes the single key the Get benchmarks read back.
func seedEntry(b *testing.B, m *Memory, value []byte, ttl time.Duration) {
	b.Helper()
	if err := m.Set(context.Background(), "k", value, ttl); err != nil {
		b.Fatal(err)
	}
}

// Set takes the write lock, so it is the operation that serializes concurrent
// traffic. Measuring it across cache sizes is how you learn whether the map
// growth shows up under load.
func BenchmarkMemory_Set(b *testing.B) {
	ctx := context.Background()
	value := []byte("v")

	for _, n := range []int{1, 1000} {
		b.Run(fmt.Sprintf("existing=%d", n), func(b *testing.B) {
			m := NewMemory()
			for i := range n {
				if err := m.Set(ctx, fmt.Sprintf("seed-%d", i), value, time.Hour); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := m.Set(ctx, "k", value, time.Hour); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Get under contention is the number that matters for a multi-core pod: the
// RWMutex must let readers proceed in parallel. A regression to a plain Mutex
// would show up here and nowhere else.
func BenchmarkMemory_GetParallel(b *testing.B) {
	ctx := context.Background()
	m := NewMemory()
	if err := m.Set(ctx, "k", []byte("v"), time.Hour); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := m.Get(ctx, "k"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
