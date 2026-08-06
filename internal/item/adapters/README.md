# internal/item/adapters

Implementations of the ports `internal/item` declares in `ports.go`
(`Storer`, `EventPublisher`). This package depends on `internal/item`;
`internal/item` never depends on it. Files are named for the *role* they
play, not the concrete technology, per the root `CLAUDE.md` decision table.

## What's here

- `database.go` — `Database`, a goroutine-safe in-memory `item.Storer` (a
  `map[string]item.Item` behind a `sync.RWMutex`). This is the
  zero-infrastructure default the template ships with. **It is one map per
  process** — running more than one replica against it silently splits data
  across pods (see the "Scaling caveat" in the root `README.md`).
- `cache.go` — `CacheStore`, a read-through decorator that fronts another
  `item.Storer` with an `internal/cache.Cache`. Wraps `Database` (or a real
  DB) when `CACHE_DRIVER` is set; single-item reads are cached, list queries
  always hit the underlying store (page invalidation isn't tractable). Cache
  failures degrade to a miss/direct read — the wrapped store stays the
  source of truth.
- `events.go` — `Publisher`, an `item.EventPublisher` that marshals domain
  events to JSON and publishes them via `internal/messaging.Publisher` on a
  fixed topic.

## When to extend

- **Swap `Database` for a real database** (Postgres, DynamoDB, ...): add a
  new file here (e.g. `postgres.go`) implementing `item.Storer` with
  parameterized queries only, then change the one constructor call in
  `internal/cli/server.go` (`adapters.NewDatabase()` → your new
  constructor). Nothing else in the module changes — that's the point of
  the domain owning `Storer`.
- **Swap the event broker**: `events.go`'s `Publisher` already only depends
  on `internal/messaging.Publisher` (the interface), so swapping the
  concrete broker means swapping what `internal/messaging.New` constructs —
  this file is usually unchanged.
- Every new file here gets a compile-time proof: `var _ item.Storer =
  (*YourType)(nil)` (see the pattern at the top of `database.go` and
  `cache.go`).

## Detaching

- Remove caching only: don't touch this package — set `CACHE_DRIVER=none`
  (or leave it unset) at the composition root; `cache.go` simply isn't
  constructed.
- Remove messaging entirely: delete `events.go`, drop the
  `adapters.NewPublisher(...)` call in `internal/cli/server.go`, and pass a
  no-op `item.EventPublisher` (or set `MESSAGING_DRIVER=none`, which already
  yields a no-op `Publisher` without any code change here).
