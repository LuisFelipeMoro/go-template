# internal/messaging

Broker-agnostic messaging contracts plus an in-memory implementation. Real
brokers (SQS, SNS, Kafka) are integrated by implementing `Publisher` and
`Consumer` in a sibling file/package — no broker SDK types may leak through
these interfaces, so swapping brokers never touches domains, adapters, or
the worker.

## What's here

- `messaging.go` — `Message` (the transport-neutral unit: `Topic`, `Key`,
  opaque `Payload` bytes), `Handler`, and the `Publisher`/`Consumer`
  interfaces every domain adapter and the worker depend on.
- `memory.go` — `Memory`, an in-process `Publisher`+`Consumer` for local dev
  and tests: one buffered channel per topic, competing consumers (shared
  queue, not fan-out). A full buffer fails `Publish` loudly rather than
  blocking or silently dropping.
- `discard.go` — `Discard`, a no-op `Publisher`+`Consumer` for
  `MESSAGING_DRIVER=none`: `Publish` drops the message, `Consume` blocks
  until its context is cancelled.
- `factory.go` — `New(driver)` selects `Memory`/`Discard` by a plain driver
  string (`memory`|`none`) and returns the `io.Closer` to release on
  shutdown. Takes a string, not a config type, so this foundation package
  never imports application code.

## When to extend

Add a broker by implementing `Publisher` and `Consumer` in this package (or
a sub-package) — no SDK types in the interfaces — and add one `case` to
`factory.go`'s `New`. Callers depend on the interfaces, so nothing upstream
changes.

## Detaching

Set `MESSAGING_DRIVER=none` and messaging costs nothing — the domain gets a
no-op publisher and the worker idles on a no-op consumer, no code deletion
needed. To remove messaging from the module entirely: delete the `worker`
command, `internal/worker`, `internal/item/adapters/events.go`, this
package, and the messaging lines in `internal/cli/server.go`.
