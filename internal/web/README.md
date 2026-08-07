# internal/web

The domain-agnostic HTTP kernel: a gin server that knows nothing about
`item` or any other domain. It owns routing plumbing (health checks, the
versioned `/v1` group, the middleware seam, request binding, the error
envelope) and serves whatever domains are registered via `RouteRegister` —
it never imports a domain package.

## What's here

- `server.go` — `Server`, `NewServer`, and the `RouteRegister`/`Option`
  seams. `WithGroupMiddleware` accepts the chain the composition root
  assembled from `internal/middleware`; `WithRoutes` accepts one
  `RouteRegister` per domain (e.g. `internal/item/http.Handler`). `Start`
  binds the listener and flips readiness on; `Stop` flips readiness off
  then drains in-flight requests — this is what makes shutdown graceful
  (see the Kubernetes Lifecycle section in the root `ARCHITECTURE.md`).
- `config.go` — `Config`, the server's tunables (port, transport timeouts,
  env, version), sourced from `internal/config` by the composition root.
  Note there is deliberately **no** body-size field: the limit is enforced by
  `middleware.BodyLimit`, and carrying the value here would imply the server
  enforces something it does not. Likewise the per-request deadline lives in
  `middleware.Timeout`, not here.
- `bind.go` — `BindJSON[T]`, the one JSON-decode path every handler uses:
  `encoding/json/v2`'s `UnmarshalRead` + `RejectUnknownMembers`, mapping
  oversized bodies to 413 and malformed JSON to 400.
- `errors.go` — `Error` and the `Code*` constants: the single error-envelope
  renderer every domain adapter and middleware calls.
- `dto.go` — `ErrorResponse` (the one error envelope shape for every
  4xx/5xx across all domains) and `healthResponse`.
- `health.go` / `readiness.go` — `/healthz` (liveness) and `/readyz`
  (readiness) handlers, and the `Readiness` flag they read. These routes
  bypass the middleware chain so probe traffic never hits metrics/logs.
- `context.go` — `StoreRequestID`/`RequestIDFromContext`: request-id
  plumbing shared between the middleware package and this package's error
  renderer, kept here (not in `internal/middleware`) so `Error` can stamp a
  request id without an import cycle.

## Sibling package

- `client/` — an *outbound* resilient HTTP client (retry + circuit breaker +
  OTel), unrelated to serving requests. See `internal/web/client/README.md`.

## When to extend

Adding a new domain's HTTP surface never touches this package — implement
`RouteRegister` in the domain's own `http/` subpackage and pass it to
`web.WithRoutes` at the composition root. Only touch this package for
transport-wide concerns (a new probe route, a new envelope field, a new
bind helper).

## Detaching

Delete `internal/web` + every domain's `http/` subpackage + `internal/middleware`
+ `newServerCmd()` in `internal/cli` to run a consumer-only service (keep
`internal/worker`). The worker is fully independent of this package.
