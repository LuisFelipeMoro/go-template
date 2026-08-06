# pkg/resilience

Context-aware building blocks for outbound calls: bounded retry and a
circuit breaker. Both are generic and carry no dependency on anything under
`internal/` — safe to import from outside this module.

## What's here

- `retry.go` — `Retry[T]`, a generic retry with bounded attempts,
  exponential backoff + jitter, a `Permanent(err)` wrapper to short-circuit
  retryable-vs-not decisions, and immediate return on context cancellation.
- `breaker.go` — `CircuitBreaker`, a 3-state breaker
  (`StateClosed`→`StateOpen` after N consecutive failures
  →`StateHalfOpen` after a cooldown, admitting bounded probe calls →closed
  again on a successful probe). `Execute(ctx, fn)` runs `fn` unless the
  breaker rejects it with `ErrOpen`. All state is mutex-serialized, so one
  breaker is safe for concurrent use.

## When to use

Compose them around any outbound call — see `internal/web/client.Client`
for the reference usage (retry wraps the breaker, which wraps the actual
`http.Client.Do`):

```go
result, err := resilience.Retry(ctx, retryCfg, func(ctx context.Context) (Quote, error) {
    var q Quote
    return q, breaker.Execute(ctx, func(ctx context.Context) error { /* outbound call */ })
})
```

Construct **one breaker per downstream dependency**, not a shared global —
breaker state should be scoped so one flaky dependency doesn't trip the
breaker for an unrelated one.

## Why this is under `pkg/`, not `internal/`

Zero dependency on `internal/` — same bar as `pkg/uid`. Both `BreakerConfig`
and `RetryConfig` are plain structs with sane zero-value defaults, so a
caller can use either with no configuration at all.

## Detaching

Standalone; only `internal/web/client` currently depends on it. Delete it
if nothing in your service makes resilient outbound calls — `client.go`
would need to go with it (or be rewritten without retry/breaker).
