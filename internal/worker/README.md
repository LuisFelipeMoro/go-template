# internal/worker

Runs the async execution path: consumes messages from a broker, processes
each within a bounded timeout, recovers from handler panics, and drains
cleanly on shutdown. Domain-agnostic — the message handler is injected by
the caller (the composition root), so this package never imports a business
domain. It's a reference for a real SQS/Kafka consumer loop.

## What's here

- `worker.go` — `Handler` (the injected per-message callback), `Metrics`
  (the consumer-owned interface `internal/telemetry.WorkerMetrics`
  implements), `Worker`, `New`, `Run`. `Run` consumes until its context is
  done or the bus closes (both clean shutdowns). Each message gets its own
  `context.WithTimeout` and panic recovery — a single poison message or
  panicking handler never stops the loop; it's logged and counted, not
  retried (the in-memory bus has no redelivery — a real broker adapter is
  where nack/redelivery semantics belong).

## Why the handler is injected, not owned here

`internal/cli/worker.go`'s `runWorker` builds the handler closure (decode →
dispatch) and passes it to `worker.New`. This keeps domain decoding/
dispatch in the composition root's line of sight instead of buried in a
foundation package — extend that closure to dispatch on `evt.Type` when you
add real event handling.

## Detaching

The template ships with an in-memory bus, so nothing external publishes to
it — `worker` is a runnable skeleton until you point `internal/messaging.New`
at a real broker. To remove it entirely: delete this package,
`newWorkerCmd()` and `worker.go` in `internal/cli`. The HTTP server is fully
independent and keeps working.
