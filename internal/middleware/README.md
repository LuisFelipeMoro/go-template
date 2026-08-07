# internal/middleware

Composable gin middlewares for the HTTP server. Each is an exported
constructor returning a `gin.HandlerFunc`; the composition root
(`internal/cli/server.go`) assembles them into the `/v1` chain in a specific
order and hands the chain to `internal/web.NewServer` via
`web.WithGroupMiddleware`. This keeps `internal/web` a pure engine with zero
hardcoded middleware — it applies whatever chain it's given.

## What's here

- `middleware.go` — `RequestID` (correlation id, first in the chain so every
  layer can read it), `SecurityHeaders` (`nosniff`, `DENY`, `no-store`, plus
  a locked-down `Content-Security-Policy: default-src 'none'` and
  `Referrer-Policy: no-referrer` — an API renders nothing, so every directive
  denies; HSTS is left to the ingress that terminates TLS), `BodyLimit`,
  `Telemetry` (one metric per response + a log **only** on 5xx, per the
  errors-only logging policy — see ADR-6 in `ARCHITECTURE.md`), `Recovery`
  (panic → 500, logged without leaking to the client). Also declares
  `Metrics`, the consumer-owned interface `internal/telemetry.HTTPMetrics`
  implements.
- `timeout.go` — `Timeout`, a per-request deadline on the **request
  context** (`HTTP_HANDLER_TIMEOUT`). This is not what `http.Server`'s
  read/write deadlines do: those are enforced by the transport and merely
  sever the connection, leaving the handler goroutine running and still
  holding its throttle slot and downstream connections. Cancelling the
  context is what actually stops the work. It writes no response itself —
  that would race a handler already writing — so the expired deadline
  surfaces as `context.DeadlineExceeded` and each domain's error mapper
  renders it (`internal/item/http` → 504). Keep the value below
  `HTTP_WRITE_TIMEOUT` so the error envelope is written before the
  transport cuts the connection.
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
`[otelgin tracing]` → `[Auth]` → `Timeout` → `BodyLimit`. RequestID first so
the id reaches every layer; Telemetry high so it observes
401/429/503/recovered-500 below it; Throttle and Auth reject before reaching
handlers; `Timeout` after them so a rejected request is never charged against
the handler deadline, and before `BodyLimit` so the deadline also covers
request decoding. Bracketed entries are config-gated.

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
