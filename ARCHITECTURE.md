# Architecture Guide

Production-grade Go 1.26 microservice template: standard Go layout
(`cmd/ → internal/ → pkg/`), package-by-domain organization, explicit
constructor dependency injection, agnostic infrastructure boundaries,
full-context propagation, and a Kubernetes-native lifecycle.

## Layers

`cmd/` is a one-line entrypoint; `internal/` holds every private
application package, organized **by domain** (a vertical slice per domain,
not split across parallel top-level dirs); `pkg/` holds the handful of
packages with zero internal dependencies, genuinely reusable as a library.

```
┌──────────────────────────────────────────────────────────────────┐
│ cmd/main.go   ENTRYPOINT — func main() { cli.Execute() }, nothing │
│               else. Never constructs a concrete type itself.      │
├──────────────────────────────────────────────────────────────────┤
│ internal/cli/       composition root — cobra tree, runServer,     │
│                     runWorker. The ONLY place concrete types are  │
│                     constructed (DI root).                        │
├──────────────────────────────────────────────────────────────────┤
│ internal/item/      DOMAIN (package-by-domain vertical slice)     │
│   item.go, service.go, ports.go, errors.go, events.go             │
│     OWNS its ports:  Storer, EventPublisher (ports.go)             │
│   adapters/          Database, CacheStore, Publisher — implement   │
│                       item's ports, depend on item, never reverse │
│   http/               HTTP adapter: Handler, request/response      │
│                        models, error mapping                      │
├──────────────────────────────────────────────────────────────────┤
│ internal/config, auth, middleware   application-layer support     │
│   (env config · bearer auth · gin middleware chain)                │
├──────────────────────────────────────────────────────────────────┤
│ internal/web, web/client, cache, messaging, telemetry, logger,     │
│ lifecycle, worker   template-specific infrastructure kernel       │
│   (imports nothing above it)                                      │
├──────────────────────────────────────────────────────────────────┤
│ pkg/uid, pkg/resilience   zero-dependency generic libraries        │
│   (safe to import from outside this module)                       │
└──────────────────────────────────────────────────────────────────┘
```

**The dependency rule**: `internal/item` (the domain) imports only the
standard library plus `pkg/uid` — it never imports `internal/item/adapters`,
`internal/item/http`, or anything else in `internal/`. The HTTP adapter
(`internal/item/http`) and the adapters (`internal/item/adapters`) depend on
`internal/item`, never the reverse. `internal/*` infrastructure packages
(`web`, `cache`, `messaging`, ...) import nothing above them; the worker takes
its message handler by injection so it stays domain-free. `pkg/*` packages
import nothing from `internal/` at all — that's what makes them safe to expose.

**Naming**: no `*bus`/`*app`/`*db` suffix convention. Packages are
disambiguated by import path instead (`internal/item` vs `internal/item/http`
vs `internal/item/adapters`) — idiomatic Go, and it means a domain's package
name never has to encode its own layer.

## Dependency Injection

No framework, no globals, no magic — plain constructors wired in one place:

```go
// internal/cli/server.go (the composition root)
var storer item.Storer = adapters.NewDatabase()                 // in-memory store
if cacheEnabled { storer = adapters.NewCache(storer, …) }        // cache alongside the db
publisher, _, busCloser, _ := messaging.New(cfg.Messaging.Driver)
svc := item.NewService(log, storer, adapters.NewPublisher(publisher, "items"))
chain := []gin.HandlerFunc{middleware.RequestID(), middleware.Telemetry(log, m), …}
server := web.NewServer(httpCfg, log, ready,
    web.WithGroupMiddleware(chain...), web.WithRoutes(itemhttp.NewHandler(svc)))
```

`runServer` and `runWorker` share one bootstrap helper
(`internal/cli/bootstrap.go`, `newObservability`) for the logger + telemetry
singletons instead of each hand-rolling identical setup — pure construction
dedup. It does not change lifecycle ownership: `server` and `worker` are
always separate OS processes, and each still registers and drains its own
`lifecycle.Runner` components independently.

Infrastructure is swapped by env, not by editing call sites:

- **Storage** — the store is constructed directly (`adapters.NewDatabase()`); a single driver needs no factory. Swap it for a real DB by adding a new adapter in `internal/item/adapters` and changing one line here.
- **Cache** — `internal/cache.New(ctx, cfg)` selects a `Cache` by `CACHE_DRIVER` (`none` | `memory` | `redis`); `adapters.NewCache` decorates the store with it. It runs *alongside* the database, never replaces it.
- **Messaging** — `internal/messaging.New(driver)` returns `Publisher, Consumer, io.Closer`. `MESSAGING_DRIVER=none` yields a no-op to detach messaging.
- **Outbound** — `internal/web/client` wraps the standard client with OTel + retry + breaker for BFF/ingestion downstream calls.

Because the domain owns its interfaces, none of this reaches it: swapping Postgres for Mongo, or Kafka for SQS, changes exactly one constructor/case and one env var.

## Startup Flow

`main` never starts the HTTP server directly — it stays a one-line delegator.
The gin server is built and served four hops down, inside the `server`
command's composition root. Tracing the exact path:

```
cmd/main.go                         cli.Execute()
  └─ internal/cli/root.go           newRootCmd().Execute()   → cobra dispatches "server"
      └─ internal/cli/server.go     RunE → runServer(ctx, cfg)   ← DI composition root
          ├─ middleware.RequestID()/…  assemble the /v1 middleware chain (internal/middleware)
          ├─ web.NewServer(...)     build the gin engine (internal/web): apply chain + mount routes
          └─ runner.Run(ctx)        start the registered "http" lifecycle component
              └─ internal/web  Server.Start(ctx)
                  ├─ net.Listen("tcp", addr)      bind the listener
                  ├─ ready.SetReady(true)         flip /readyz → 200
                  └─ http.Serve(ln)               ← gin actually serves (http.Server.Handler = the engine)
```

Two distinct steps: `web.NewServer` **builds** the engine (applies the middleware chain the composition root assembled, then mounts each `RouteRegistrar`'s routes onto `gin.New()`); `Start` **serves** it. Because the server is registered as a `lifecycle.Component{Start: server.Start, Stop: server.Stop}`, `Start` blocks until the context is cancelled and then returns — the runner calls `Stop` next, which flips readiness off and drains in-flight requests. This is why shutdown is graceful instead of a hard `main` exit: `main` calls no `ListenAndServe` and owns no server.

## Context Propagation

`context.Context` is the first parameter of every function across every layer:

```
signal.NotifyContext (SIGTERM/SIGINT)
  └─ lifecycle.Runner.Run(ctx)
       ├─ web.Server.Start(ctx) → gin → otelgin (span in ctx) → handler(c.Request.Context())
       │    └─ item.Service.Create(ctx, …) → adapters.Database.Create(ctx, …)
       │                                   → adapters.Publisher.Publish(ctx, …)
       └─ worker.Run(ctx) → messaging.Consume(ctx) → per-message ctx (timeout)
```

- **Tracing**: otelgin opens a span and stores it in the request context; every downstream call carries it; `internal/logger`'s `TraceHandler` extracts `trace_id`/`span_id` into any log record emitted with that context — logs and traces correlate with zero per-call effort. OTLP export feeds any backend (Datadog, Jaeger, Tempo).
- **Cancellation**: client disconnect or shutdown cancels the request context; storage and publishers honor it.
- **Timeouts**: the worker wraps each message in `context.WithTimeout`; HTTP timeouts are enforced by `http.Server` read/write deadlines.

## Observability Policy (ADR-6): errors-only logging

Production paths emit logs **only at `error` level**. Every non-error signal is an OpenTelemetry **metric**:

| Signal | Vehicle |
|--------|---------|
| Request count / latency / status | `http.server.requests`, `http.server.duration` metrics |
| Worker throughput | `worker.messages.processed` / `worker.messages.failed` counters |
| 5xx, panics, handler failures | one slog `Error` with `request_id` + `trace_id` |
| Success paths | no log — metrics + traces only |

`LOG_LEVEL` defaults to `error`; local development (compose) opts into `debug`.

## Resiliency

`pkg/resilience` provides context-aware building blocks for outbound calls:

- `Retry[T]` — bounded attempts, exponential backoff + jitter, permanent-error short-circuit, stops on context cancellation.
- `CircuitBreaker` — closed → open after N consecutive failures → half-open probes after cooldown → closed on success. Fails fast with `ErrOpen`.

Compose them around any client call:

```go
result, err := resilience.Retry(ctx, retryCfg, func(ctx context.Context) (Quote, error) {
    var q Quote
    return q, breaker.Execute(ctx, func(ctx context.Context) error { /* outbound call */ })
})
```

**Event consistency note**: the domain publishes events best-effort after a successful write (publish failure is logged, the operation still succeeds). For exactly-once semantics, upgrade to a transactional outbox when a real database lands — the seam is `item.EventPublisher`.

## Kubernetes Lifecycle

Graceful shutdown maps 1:1 onto the pod termination sequence:

```
kubelet sends SIGTERM
  → signal ctx canceled → lifecycle.Runner begins reverse-order stop
  → readiness flips: /readyz returns 503        (pod leaves Service endpoints)
  → http.Server.Shutdown drains in-flight requests   (listener stays open)
  → worker finishes its in-flight message, stops pulling
  → bus closed → telemetry flushed (spans/metrics exported)
  → process exits 0 — all inside SHUTDOWN_TIMEOUT (20s)
terminationGracePeriodSeconds: 30 > 20s  → SIGKILL never races the drain
```

Probes (`ops/k8s/base/deployment.yaml`):
- **liveness** `GET /healthz` — process alive; restarts only on hang/crash.
- **readiness** `GET /readyz` — traffic gate; 503 during startup and drain.

The image is distroless static (no shell), so container health uses the binary itself: `app healthcheck`. Scaling is HPA-owned (CPU 70%, 1–10 replicas). The HTTP/worker processes themselves hold no state, but the reference `Storer` (`adapters.Database`, in-memory) does — one map per pod, not shared. `minReplicas` defaults to 1 for this reason: running more than one replica against the in-memory store splits data silently (an item created on one pod won't be visible from another). Swap `Storer` for a real shared database (see the Dependency Injection section above) before raising `minReplicas`.

## GitOps & Configuration

Config is read exclusively from the environment (12-factor), which makes the deployment declarative and git-managed rather than imperative:

```
git (source of truth)
  ops/k8s/base/                 manifests + configMapGenerator (hashed name)
  ops/k8s/overlays/{dev,        per-env overrides (behavior: merge), image
    staging,prod}/                 tag, namespace, scaling
  ops/k8s/overlays/prod/        ExternalSecret — references only, no plaintext
    external-secret.yaml           (External Secrets Operator materializes the Secret)
  ops/argocd/application.yaml   ArgoCD watches an overlay, auto-syncs on merge
        │
        ▼  controller (ArgoCD/Flux) reconciles
  cluster: ConfigMap(-<hash>) + Secret → envFrom → the app's config.Load()
```

Changing prod config is a PR against the `configMapGenerator` literals in `overlays/prod/kustomization.yaml`. The content hash in the ConfigMap name changes → the Deployment's reference changes → Kubernetes performs a rolling update, so the new value actually reaches running pods **with no operator** (a plain ConfigMap would not restart them). Secrets never enter git in plaintext — only references, resolved at reconcile time from your store (swap ESO for SOPS/sealed-secrets; the Deployment depends only on the resulting Secret name; rotation auto-roll needs Reloader). The Go code is unchanged across all of this — it just reads env. `ops/README.md` covers the app-repo-vs-config-repo split for scale.

## Extending the Template

See [CLAUDE.md](CLAUDE.md) for step-by-step recipes: adding a domain, adding/removing a storage or bus adapter, detaching the worker or HTTP server, and the quality gates every change must clear.
