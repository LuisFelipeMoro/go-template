# internal/middleware

Composable gin middlewares for the HTTP server. Each is an exported
constructor returning a `gin.HandlerFunc`; the composition root
(`internal/cli/server.go`) assembles them into the `/v1` chain in a specific
order and hands the chain to `internal/web.NewServer` via
`web.WithGroupMiddleware`. This keeps `internal/web` a pure engine with zero
hardcoded middleware — it applies whatever chain it's given.

## What's here

- `middleware.go` — `RequestID` (correlation id, first in the chain so every
  layer can read it), `SecurityHeaders`, `BodyLimit`, `Telemetry` (one
  metric per response + a log **only** on 5xx, per the errors-only logging
  policy — see ADR-6 in `ARCHITECTURE.md`), `Recovery` (panic → 500,
  logged without leaking to the client). Also declares `Metrics`, the
  consumer-owned interface `internal/telemetry.HTTPMetrics` implements.
- `auth.go` — `Auth`, gating the chain on a valid bearer token; declares
  `Authenticator`, the consumer-owned interface `internal/auth.StaticKeys`
  (or any replacement) implements. Failure is always a generic 401 — never
  distinguishing "missing token" from "wrong token" in the response.
- `throttle.go` — `Throttle`, a per-instance concurrency cap (backpressure,
  not a rate limit — a request-per-second quota belongs at the
  gateway/ingress, enforced globally across replicas). `Semaphore` is the
  built-in `Limiter`; any concurrency limiter can be injected.

## Ordering (as wired in `internal/cli/server.go`)

`RequestID` → `SecurityHeaders` → `Telemetry` → `Recovery` → `[Throttle]` →
`[otelgin tracing]` → `[Auth]` → `BodyLimit`. RequestID first so the id
reaches every layer; Telemetry high so it observes 401/429/503/recovered-500
below it; Throttle and Auth reject before reaching handlers; BodyLimit last,
just before routes.

## When to extend

Add a constructor here returning `gin.HandlerFunc`, then insert it into the
chain in `internal/cli/server.go` at the position you need — this package
never assembles its own chain, only provides the pieces.

## Detaching

Individual middlewares are opt-in via config (`AUTH_ENABLED`,
`THROTTLE_ENABLED`) — leaving them disabled means they're simply never
appended to the chain, no code deletion needed. The whole package only
matters if you keep the HTTP server; see `internal/web/README.md` for
removing the server (and this package) entirely.
