# internal/lifecycle

A single place to run a set of long-lived components and shut them down
gracefully. Both composition roots (`runServer`, `runWorker` in
`internal/cli`) build one `Runner`, register their components, and call
`Run` — this is what makes Kubernetes-native graceful shutdown (SIGTERM →
drain → exit) uniform across the server and the worker instead of each
hand-rolling it.

## What's here

- `runner.go` — `Component` (`Name`, `Start`, `Stop`), `Runner`, `NewRunner`,
  `Add`, `Run`. Components start concurrently via `errgroup`; `Run` blocks
  until SIGINT/SIGTERM, a cancelled context, or the first fatal `Start`
  error, then stops every component **in reverse registration order** under
  a fresh timeout-bounded context, joining any stop errors. `Stop` must be
  idempotent; `nil` is allowed when there's nothing to tear down.

## Why reverse-order stop matters

Registration order is start order; the reverse is stop order. In
`runServer`, `telemetry` is registered first and `http` last — so on
shutdown the HTTP server drains first, then the bus closes, then telemetry
flushes, capturing every span/metric from the drain. Get the registration
order right when you add a new component: whatever should keep working
while other things drain must be registered *before* them.

## When to extend

Register a new long-lived dependency (another consumer, another server) as
a `lifecycle.Component` at the composition root — this package itself
rarely needs changes; `Component` is generic enough for any start/stop
pair.

## Detaching

Not applicable — it's the shutdown mechanism both `server` and `worker`
depend on. Load-bearing infrastructure.
