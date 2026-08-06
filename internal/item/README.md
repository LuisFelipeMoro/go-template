# internal/item

The reference domain — copy this tree's shape for every new domain you add.
`item` is a package-by-domain vertical slice: everything about the `item`
aggregate (entity, invariants, business operations, the contracts it needs
from the outside world) lives in this one directory. Nothing about `item`
lives anywhere else in the module.

## What's here

- `item.go` — the `Item` entity, `NewItem`/`UpdateItem`/`Page` value types,
  and field-level invariant validation (name length, non-negative
  quantity/price, page bounds). This is where business rules live.
- `service.go` — `Service`, the use-case layer: `Create`/`QueryByID`/
  `Query`/`Update`/`Delete`, each validating input, calling the store, and
  publishing a domain event best-effort on success.
- `ports.go` — `Storer` and `EventPublisher`, the interfaces this package
  **owns** as the consumer. Any storage or broker implementation satisfies
  them without this package changing — see "Domain owns its contracts" in
  the root `ARCHITECTURE.md`.
- `errors.go` — sentinel errors (`ErrNotFound`, `ErrInvalidArgument`) that
  callers inspect with `errors.Is`, never by string.
- `events.go` — the `Event` envelope and `item.created`/`item.updated`/
  `item.deleted` type constants. Events carry identifiers only, never the
  full entity.

## Dependency rule

This package imports **only** the standard library plus `pkg/uid`. It never
imports `internal/item/adapters`, `internal/item/http`, gin, a broker SDK, or
anything else under `internal/`. That's what makes storage/broker swaps a
one-file change elsewhere instead of a refactor here.

## Sibling packages

- `adapters/` — implements `Storer` and `EventPublisher` (in-memory DB, cache
  decorator, messaging publisher). Depends on `item`, never the reverse.
- `http/` — the HTTP adapter: request/response models, error mapping, route
  registration. Depends on `item`, never the reverse.

## When to extend

- **New field/invariant on Item**: add it in `item.go`'s `validate()`
  methods — the domain is where invariants belong, not the HTTP layer or the
  adapter.
- **New use case**: add a method to `Service` in `service.go`.
- **New entity that's part of the item aggregate** (no independent
  lifecycle/storage of its own): add it to this package, not a new
  directory.
- **New domain entirely** (its own aggregate, lifecycle, storage): don't add
  to this package — copy this whole tree as `internal/<name>/` instead. See
  the root `CLAUDE.md` → "Adding a new domain."

## Detaching

Not applicable — this is the sample domain the template ships with. Delete
it (and its `adapters/`, `http/`, and wiring in `internal/cli/server.go`) if
you don't want a worked example, but a real service needs at least one
domain here.
