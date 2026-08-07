// factory.go
package cache

import (
	"context"
	"fmt"
	"io"
)

// Config selects and configures the cache backend. It carries no domain
// knowledge and no internal/config dependency (pkg stays domain-agnostic), so
// the composition root maps its typed config onto these plain fields.
type Config struct {
	Driver string // none (default) | memory | redis
	Redis  RedisConfig
}

// RedisConfig holds Redis connection settings. Password is a secret: sourced
// from the environment, never logged, never committed.
type RedisConfig struct {
	Addr     string // host:port
	Password string // secret
	DB       int    // logical database index
	// TLS enables encryption in transit. Off by default because a local Redis
	// in docker-compose speaks plaintext, but any managed Redis reached over a
	// network (Elasticache/MemoryDB in-transit encryption, Upstash, Redis
	// Cloud) must set it — otherwise the AUTH password and every cached value
	// cross the network in the clear.
	TLS bool
}

// noopCloser satisfies io.Closer for backends with nothing to release.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// New builds the Cache named by cfg.Driver and an io.Closer the lifecycle owns.
//
//   - "none"   → NewNoop: caching disabled (pass-through).
//   - "memory" → NewMemory: in-process TTL cache.
//   - "redis"  → NewRedis: shared Redis-compatible cache (dials + pings).
//
// To add a backend: implement Cache and add one case — call sites are unchanged
// because they depend on Cache, not the concrete type.
func New(ctx context.Context, cfg Config) (Cache, io.Closer, error) {
	switch cfg.Driver {
	case "", "none":
		return NewNoop(), noopCloser{}, nil
	case "memory":
		return NewMemory(), noopCloser{}, nil
	case "redis":
		c, err := NewRedis(ctx, cfg.Redis)
		if err != nil {
			return nil, nil, fmt.Errorf("building redis cache: %w", err)
		}
		return c, c, nil
	default:
		return nil, nil, fmt.Errorf("unknown cache driver %q (supported: none, memory, redis)", cfg.Driver)
	}
}
