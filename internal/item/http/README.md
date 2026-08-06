# internal/item/http

The item domain's HTTP adapter. Maps HTTP requests to `item.Service` calls
and domain errors to HTTP responses; registers its routes on the kernel's
`/v1` group via `internal/web.RouteRegistrar`. Imported package name is
`http`, so callers alias it (`itemhttp "github.com/.../internal/item/http"`)
wherever `net/http` is also imported in the same file.

This package depends on `internal/item`; `internal/item` never depends on
it. It carries no business rules of its own — invariant checks live in the
domain (`internal/item/item.go`), not here.

## What's here

- `handler.go` — `Handler`, implementing `web.RouteRegistrar`. One method
  per operation (`create`/`list`/`get`/`update`/`delete`), each decoding the
  request, calling the service, and rendering the response.
- `model.go` — the wire DTOs (`createItemRequest`, `updateItemRequest`,
  `itemResponse`, `itemListResponse`) and the domain↔DTO mapping
  (`toItemResponse`). Kept separate from `handler.go` so request/response
  shape changes don't touch handler logic.
- `pagination.go` — query-param parsing for `page`/`rows` (defaults 1/20,
  out-of-range values rejected with 400 rather than silently clamped).
- `errors.go` — `mapDomainError`: the single place that turns
  `item.ErrInvalidArgument`/`item.ErrNotFound`/anything else into the
  correct `web.Error` status + code. Internal failures collapse to a generic
  500 so implementation detail never reaches the client.

## When to extend

- **New endpoint on this domain**: extend `api-spec.yaml` first, run `make
  spec-lint`, then add a handler method + route in `handler.go`'s
  `Register`, plus any new DTOs in `model.go`.
- **New domain error to map**: add a `case` in `errors.go`'s
  `mapDomainError` — inspect with `errors.Is`, never a string match.

## Detaching

Not independently detachable — it's the HTTP surface for one domain. Delete
it along with the domain (`internal/item/`) and its wiring in
`internal/cli/server.go`, or delete the whole HTTP server (see
`internal/web/README.md`) if you're going worker-only.
