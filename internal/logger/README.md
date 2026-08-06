# internal/logger

Builds the application's structured logger: single-line JSON via stdlib
`slog`, wrapped so every record automatically carries `trace_id`/`span_id`
when the caller's context holds a valid OpenTelemetry span — logs and
traces correlate with zero per-call effort.

## What's here

- `logger.go` — `Config` and `New(cfg)`. Writes JSON to `cfg.Writer`
  (defaults to stdout) at `cfg.Level` (`debug`|`info`|`warn`|`error`;
  unknown falls back to `info`), with `service`/`version`/`env` base
  attributes.
- `trace_handler.go` — `TraceHandler`, a `slog.Handler` wrapper that injects
  `trace_id`/`span_id` at the JSON root (even through caller `WithGroup`
  calls) whenever the handling context carries a valid span, and passes
  records through unchanged otherwise.

## Policy this package exists to support

**Errors-only logging (ADR-6, non-negotiable)**: production paths log
**only** at `error` level, always with `request_id`/`trace_id`, never
PII/secrets/payloads. Every non-error signal (counts, durations,
throughput) is an OpenTelemetry metric via `internal/telemetry`, not a log
line. `LOG_LEVEL` defaults to `error`; local dev (`compose`) opts into
`debug`. This package never logs a value on its own — callers must not pass
PII/secrets/tokens/card data as log attributes.

## Detaching

Not applicable — every command constructs one logger via
`internal/cli/bootstrap.go`'s shared `newObservability` helper. It's
load-bearing infrastructure, not an optional boundary.
