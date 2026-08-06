# internal/cache

A domain-agnostic caching boundary, shaped like `internal/messaging`: it
owns the `Cache` interface, a config-driven factory (`New`), and the
built-in implementations. A cache sits **alongside** the primary store — it
never replaces it, so a service can run a real database and a Redis cache
at the same time (see `internal/item/adapters/cache.go` for the decorator
that plugs a `Cache` in front of an `item.Storer`).

Callers cache by opaque key and bytes; serialization is the caller's
concern (see `item/adapters/cache.go`'s use of `encoding/json/v2`).

## What's here

- `cache.go` — the `Cache` interface (`Get`/`Set`/`Delete`, per-entry TTL,
  concurrency-safe implementations required) and `noop`, the zero-overhead
  implementation used when caching is disabled. A miss is always
  `(nil, false, nil)`, never an error, so callers fall through to the
  source of truth.
- `memory.go` — `Memory`, an in-process TTL cache with lazy expiry (checked
  on read). Safe default for a single instance or tests; **not** a
  substitute for Redis across multiple replicas, since each instance would
  hold its own copy.
- `redis.go` — `RedisCache`, backed by `github.com/redis/go-redis/v9`,
  shared across replicas. `NewRedis` pings on construction so a bad address
  fails fast at startup. The command surface is isolated behind the
  internal `redisDoer` interface so the cache logic is unit-testable
  without a live Redis server.
- `factory.go` — `New(ctx, cfg)` selects `NewNoop`/`NewMemory`/`NewRedis` by
  `cfg.Driver` (`none`|`memory`|`redis`, env var `CACHE_DRIVER`) and returns
  the `io.Closer` the composition root registers for shutdown.

This is a single boundary with a driver switch, not one package per driver
— per the root `CLAUDE.md`'s rule for infra boundaries. Don't split
`redis.go`/`memory.go` into separate packages; that would just be the same
factory pattern with more ceremony.

## When to extend

Add a backend by implementing `Cache` in a new file here and adding one
`case` to `factory.go`'s `New` — call sites depend on `Cache`, not the
concrete type, so nothing else changes.

## Detaching

Leave `CACHE_DRIVER` unset (defaults to `none`) and caching costs nothing —
`adapters.NewCache` is never constructed at the composition root. To remove
the capability from the module entirely: delete this package, the
`CacheConfig` fields in `internal/config`, `internal/item/adapters/cache.go`,
and the `if cfg.Cache.Driver != ""` block in `internal/cli/server.go`.
