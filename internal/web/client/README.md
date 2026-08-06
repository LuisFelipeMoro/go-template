# internal/web/client

A resilient **outbound** HTTP client for downstream calls — BFF-style
aggregation, ingestion fetches, webhooks to a third party. Unrelated to
`internal/web`'s inbound server; it happens to live under the same tree
because it's also HTTP infrastructure, not because it shares code with the
server.

## What's here

- `client.go` — `Client`, built on the standard `net/http.Client`, layering
  three concerns via composition rather than reinventing them:
  - **Tracing**: the transport is wrapped with `otelhttp`, so every request
    is a traced span propagating W3C context.
  - **Retry**: bounded attempts via `pkg/resilience.Retry`, retrying only a
    transport error or a `>=500`/`429` response; 4xx is returned to the
    caller unretried; a request body is only retried when `req.GetBody` is
    set.
  - **Circuit breaker**: `pkg/resilience.CircuitBreaker` wraps each attempt,
    so a persistently failing downstream fails fast (`ErrOpen`) instead of
    piling up retries against a dead dependency.

  `Client.Do` is the entry point; `Client.State()` exposes the breaker state
  for metrics/health reporting.

## When to use

Construct one `client.Client` per downstream dependency you call out to
(not one global client) — retry/breaker state should be scoped per
dependency, since one flaky downstream shouldn't trip the breaker for
another. Tune `Config.Timeout`/`Retry`/`Breaker` per call site; the
`internal/config.HTTPClientConfig` fields (`HTTP_CLIENT_TIMEOUT`,
`HTTP_CLIENT_MAX_RETRIES`) are the template's example of wiring one
instance from env.

## Detaching

Zero-dependency on the rest of `internal/web` or any domain — nothing
imports this package unless a composition root constructs a `client.Client`
for an actual downstream call. Delete the package if the service makes no
outbound HTTP calls; nothing else breaks.
