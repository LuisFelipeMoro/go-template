# internal/telemetry

Bootstraps OpenTelemetry traces and metrics over OTLP gRPC, and provides the
concrete metric recorders the HTTP and worker paths use. Vendor-neutral:
point `OTEL_EXPORTER_OTLP_ENDPOINT` at Datadog, Jaeger, Tempo, or Grafana —
no code change.

## What's here

- `telemetry.go` — `Config`, `Providers` (`Tracer`, `Meter`, `Shutdown`),
  and `Init(ctx, cfg)`. Returns the constructed providers for injection —
  the rest of the app receives them by parameter, never by reaching for a
  package-level global (though the process-wide otel globals are still set,
  for zero-config third-party instrumentation like `otelgin`/`otelhttp`).
  When `cfg.Enabled` is false, returns no-op providers so instruments stay
  cost-free and construction never depends on call order.
- `http_metrics.go` — `HTTPMetrics`, implementing
  `internal/middleware.Metrics`: `http.server.requests` counter +
  `http.server.duration` histogram, tagged by method/route-template/status
  (route template, never the raw path, to bound cardinality).
- `worker_metrics.go` — `WorkerMetrics`, implementing
  `internal/worker.Metrics`: `worker.messages.processed`/
  `worker.messages.failed` counters.

## Policy this package exists to support

**Errors-only logging (ADR-6)**: non-error operational signals (request
counts, latency, worker throughput) belong here as metrics — never as info/
warn logs. See `internal/logger/README.md` for the logging half of this
policy.

## When to extend

Add a new metric family the same way `http_metrics.go`/`worker_metrics.go`
do: a small struct holding the instruments, a `NewXMetrics(mp
metric.MeterProvider)` constructor, and methods recording to them. Consumers
declare their own narrow `Metrics` interface (see
`internal/middleware.Metrics`, `internal/worker.Metrics`) so this package
stays the only place with an OTel dependency.

## Detaching

Leave `OTEL_ENABLED=false` (the default) and every instrument is a no-op —
no code deletion needed. This package is otherwise load-bearing: both
composition roots call `telemetry.Init` via
`internal/cli/bootstrap.go`.
