// Package cache is a domain-agnostic caching boundary, shaped like internal/messaging: it
// owns the Cache interface, a config-driven factory (New), and the built-in
// implementations (noop, in-memory, Redis). A cache sits alongside the primary
// store — it never replaces it — so a service can run a real database and a
// Redis cache at the same time (see internal/item/adapters). Callers cache by
// opaque key and bytes; serialization is the caller's concern.
package cache

import (
	"context"
	"time"
)

// Cache is a best-effort key/value cache with per-entry TTL. Implementations
// must be safe for concurrent use. A miss is (nil, false, nil) — never an
// error — so callers fall through to the source of truth; errors are reserved
// for backend failures the caller may log and treat as a miss.
type Cache interface {
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Compile-time proof that every built-in backend satisfies Cache, so adding a
// method to the interface breaks here rather than at a distant call site.
var (
	_ Cache = noop{}
	_ Cache = (*Memory)(nil)
	_ Cache = (*RedisCache)(nil)
)

// noop is the zero-overhead Cache used when caching is disabled: every read
// misses and every write is dropped, so a decorated store behaves exactly like
// an undecorated one.
type noop struct{}

// NewNoop returns a Cache that stores nothing.
func NewNoop() Cache { return noop{} }

func (noop) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, nil }
func (noop) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (noop) Delete(context.Context, string) error                     { return nil }
