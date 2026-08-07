# Architecture Guide

This document explains *why* the template is shaped the way it is. It covers the
package layout and the dependency rule that holds it together, how dependencies
are wired, what happens on startup and shutdown, how observability and resiliency
are handled, and how the whole thing lands in Kubernetes.

If you only need to know *where to put new code*, read
[CLAUDE.md](CLAUDE.md) instead — it is the practical recipe book. This file is
the reasoning behind those recipes.

## Contents

| Section | Read it when you want to… |
|---|---|
| [1. The layout](#1-the-layout) | understand the directory structure and what belongs where |
| [2. The dependency rule](#2-the-dependency-rule) | know what may import what, and why the domain stays clean |
| [3. Dependency injection](#3-dependency-injection) | see how the pieces get wired together |
| [4. Startup flow](#4-startup-flow) | trace what happens between `main()` and a served request |
| [5. Context propagation](#5-context-propagation) | understand tracing, cancellation, and the two kinds of timeout |
| [6. Observability](#6-observability) | know where a given signal goes — log, metric, or trace |
| [7. Resiliency](#7-resiliency) | protect an outbound call, or understand event-delivery guarantees |
| [8. Kubernetes lifecycle](#8-kubernetes-lifecycle) | understand graceful shutdown, rollouts, and scaling limits |
| [9. GitOps and configuration](#9-gitops-and-configuration) | change config safely and understand fail-secure startup |
| [10. Architecture decisions (ADRs)](#10-architecture-decisions-adrs) | find the rationale a source comment refers to by number |

---

## 1. The layout

The template follows the standard Go community layout, with one deliberate
choice layered on top: **organization by domain, not by technical layer.**

`cmd/` is a one-line entrypoint that does nothing but delegate. `internal/`
holds every private application package, and a domain lives there as a single
vertical slice — its entity, business rules, ports, adapters, and HTTP handler
all sit under one directory tree rather than being scattered across parallel
top-level folders. `pkg/` is reserved for the handful of packages that depend on
nothing inside this module and are therefore genuinely safe to import from
outside it.

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

### A note on package names

There is no `*bus`/`*app`/`*db` suffix convention here. Packages are
disambiguated by their import path instead — `internal/item` versus
`internal/item/http` versus `internal/item/adapters`. This is the idiomatic Go
approach, and it has a pleasant consequence: a domain's package name never has
to encode its own layer.

---

## 2. The dependency rule

One rule governs the whole tree, and everything else follows from it:
**dependencies point inward, toward the domain, and never back out.**

Concretely, `internal/item` — the domain — imports only the standard library
plus `pkg/uid`. It never imports its own adapters, its own HTTP handler, or
anything else under `internal/`. The relationship is strictly one-directional:
`internal/item/http` and `internal/item/adapters` depend on `internal/item`, and
never the reverse. This is what keeps gin, Redis, and broker SDKs out of your
business logic entirely.

The infrastructure packages (`web`, `cache`, `messaging`, and friends) import
nothing above them in the diagram. The worker is a good illustration of how that
is achieved: it receives its message handler by injection, so it can process
domain events without ever importing a domain. Finally, `pkg/*` imports nothing
from `internal/` at all — that restriction is precisely what makes those
packages safe to expose as a library.

This is verifiable rather than aspirational. Running `go list` over the module
shows `internal/web` importing no internal package at all, `internal/item`
importing only `pkg/uid`, and `internal/cli` — the composition root — as the
only package that imports broadly.

---

## 3. Dependency injection

There is no DI framework, no service locator, and no global state. Dependencies
are plain constructor arguments, and every concrete type in the process is
constructed in exactly one place.

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

`runServer` and `runWorker` share a single bootstrap helper
(`newObservability` in `internal/cli/bootstrap.go`) for the logger and telemetry
providers, rather than each hand-rolling identical setup. This is construction
deduplication only — it deliberately does not centralize lifecycle ownership.
The server and worker are always separate OS processes, and each registers and
drains its own `lifecycle.Runner` components independently.

### Swapping infrastructure

Because the domain owns its interfaces, infrastructure is swapped by changing
one constructor call and one environment variable — never by editing call sites
across the codebase.

**Storage** is constructed directly (`adapters.NewDatabase()`); a single driver
needs no factory ceremony. Swapping in a real database means adding an adapter
under `internal/item/adapters` and changing one line at the composition root.

**Cache** resolves through `internal/cache.New(ctx, cfg)`, which selects a
`Cache` implementation by `CACHE_DRIVER` (`none`, `memory`, or `redis`).
`adapters.NewCache` then decorates the store with it. Note that the cache runs
*alongside* the database and never replaces it.

**Messaging** resolves through `internal/messaging.New(driver)`, which returns a
`Publisher`, a `Consumer`, and an `io.Closer`. Setting `MESSAGING_DRIVER=none`
yields no-op implementations, detaching messaging entirely.

**Outbound HTTP** is available via `internal/web/client`, which wraps the
standard client with OpenTelemetry tracing, retry, and a circuit breaker.

---

## 4. Startup flow

`main` never starts the HTTP server directly — it stays a one-line delegator.
The gin server is built and served four hops down, inside the `server` command's
composition root:

```
cmd/main.go                         cli.Execute()
  └─ internal/cli/root.go           newRootCmd().Execute()   → cobra dispatches "server"
      └─ internal/cli/server.go     RunE → runServer(ctx, cfg)   ← DI composition root
          ├─ middleware.RequestID()/…  assemble the /v1 middleware chain (internal/middleware)
          ├─ web.NewServer(...)     build the gin engine (internal/web): apply chain + mount routes
          └─ runner.Run(ctx)        start the registered "http" lifecycle component
              └─ internal/web  Server.Start(ctx)
                  ├─ net.ListenConfig{}.Listen(ctx, "tcp", addr)   bind (ctx-aware)
                  ├─ ready.SetReady(true)         flip /readyz → 200
                  └─ http.Serve(ln)               ← gin actually serves
```

Building and serving are two distinct steps. `web.NewServer` **builds** the
engine: it applies the middleware chain the composition root assembled, then
mounts each `RouteRegister`'s routes onto `gin.New()`. `Start` then **serves**
it.

That separation is what makes shutdown graceful. The server is registered as a
`lifecycle.Component{Start: server.Start, Stop: server.Stop}`, so `Start` blocks
until the context is cancelled and then returns, at which point the runner calls
`Stop` — which flips readiness off and drains in-flight requests. Because `main`
never calls `ListenAndServe` and owns no server, there is no path by which the
process can exit without draining first.

---

## 5. Context propagation

`context.Context` is the first parameter of every function across every layer,
which makes tracing, cancellation, and deadlines work without per-call effort:

```
signal.NotifyContext (SIGTERM/SIGINT)
  └─ lifecycle.Runner.Run(ctx)
       ├─ web.Server.Start(ctx) → gin → otelgin (span in ctx) → handler(c.Request.Context())
       │    └─ item.Service.Create(ctx, …) → adapters.Database.Create(ctx, …)
       │                                   → adapters.Publisher.Publish(ctx, …)
       └─ worker.Run(ctx) → messaging.Consume(ctx) → per-message ctx (timeout)
```

### Tracing

otelgin opens a span and stores it in the request context, and every downstream
call carries it from there. `internal/logger`'s `TraceHandler` extracts
`trace_id` and `span_id` from that context into any log record emitted with it,
so logs and traces correlate automatically. OTLP export feeds any backend —
Datadog, Jaeger, Tempo.

### Cancellation

A client disconnect or a shutdown signal cancels the request context, and both
storage and publishers honor it.

### Timeouts — two mechanisms, and the difference matters

This is the subtlety most services get wrong, so it is worth stating plainly.

`http.Server`'s read and write deadlines are enforced by the **transport**. When
one fires, the connection is severed — but the handler goroutine keeps running,
still holding its throttle slot, its database connection, and whatever else it
acquired. The client sees a failure; the server keeps burning the resource.

`middleware.Timeout` (`HTTP_HANDLER_TIMEOUT`, default 8s) is different in kind:
it puts a deadline on the **request context**. A stalled store, cache, or
outbound call is genuinely cancelled, and its goroutine and throttle slot are
released. This is what actually stops the work.

Keep the handler timeout **below** `HTTP_WRITE_TIMEOUT` so a slow request
renders a 504 error envelope rather than having its connection cut mid-write.
The worker has the equivalent per-message bound via `context.WithTimeout` in
`internal/worker`.

---

## 6. Observability

Production paths emit logs **only at `error` level**. Every non-error signal is
an OpenTelemetry metric. This is ADR-6, and it is not negotiable within the
template — see [the ADR](#10-architecture-decisions-adrs) for the reasoning.

| Signal | Vehicle |
|--------|---------|
| Request count / latency / status | `http.server.requests`, `http.server.duration` metrics |
| Worker throughput | `worker.messages.processed` / `worker.messages.failed` counters |
| 5xx, panics, handler failures | one slog `Error` with `request_id` + `trace_id` |
| Success paths | no log — metrics + traces only |

`LOG_LEVEL` defaults to `error`; local development via docker-compose opts into
`debug`.

The practical consequence worth internalizing: a 401 produces no log line. It is
visible as a `status` attribute on the request counter, and that metric is what
you alert on. If you find yourself wanting an info log to answer an operational
question, the answer is almost always a metric dimension instead.

---

## 7. Resiliency

`pkg/resilience` provides two context-aware building blocks for outbound calls.
`Retry[T]` gives bounded attempts with exponential backoff and jitter, a
short-circuit for permanent errors, and cancellation on context expiry.
`CircuitBreaker` moves closed → open after N consecutive failures → half-open
probes after a cooldown → closed again on success, failing fast with `ErrOpen`
while open.

They compose around any client call:

```go
result, err := resilience.Retry(ctx, retryCfg, func(ctx context.Context) (Quote, error) {
    var q Quote
    return q, breaker.Execute(ctx, func(ctx context.Context) error { /* outbound call */ })
})
```

### Event delivery is best-effort

The domain publishes events **after** a successful write, and a publish failure
is logged without failing the operation. This is a deliberate trade: the state
change already committed, so failing the caller at that point would be a lie.

The consequence is that events can be lost. For exactly-once semantics, upgrade
to a transactional outbox once a real database backs `Storer` — the seam you
extend is `item.EventPublisher`.

---

## 8. Kubernetes lifecycle

### The shutdown sequence

Graceful shutdown maps one-to-one onto the pod termination sequence:

```
pod marked Terminating
  → preStop: sleep 5s          ← endpoints propagate while the pod still serves
  → kubelet sends SIGTERM
  → signal ctx canceled → lifecycle.Runner begins reverse-order stop
  → readiness flips: /readyz returns 503        (pod leaves Service endpoints)
  → http.Server.Shutdown drains in-flight requests   (listener stays open)
  → worker finishes its in-flight message, stops pulling
  → bus closed → telemetry flushed (spans/metrics exported)
  → process exits 0 — all inside SHUTDOWN_TIMEOUT (20s)
terminationGracePeriodSeconds: 30 > 5s preStop + 20s drain → SIGKILL never races
```

### Why the preStop sleep exists

Removing a pod from Service endpoints is **asynchronous**. Without a delay, the
application stops accepting connections the instant SIGTERM arrives, while
kube-proxy and the ingress may still route to it for a beat. Those connections
are refused, and the symptom is a burst of 502s on every rolling update — the
classic "we deploy and users see errors" problem.

The five-second pause lets endpoint removal win that race. The native `sleep`
lifecycle action is used rather than `exec: sh -c sleep`, because the distroless
image ships no shell to exec.

### Rollout and disruption safety

Three further primitives keep a rollout non-disruptive, all in `ops/k8s/base/`.
`maxUnavailable: 0` in the Deployment strategy means a new pod must pass its
readiness probe before an old one is torn down. `topologySpreadConstraints`
spreads replicas across nodes so a single node failure is not an outage. And a
PodDisruptionBudget (`pdb.yaml`) caps voluntary disruption during node drains.

The PDB uses `maxUnavailable: 1` rather than `minAvailable`, and that choice is
deliberate: with the default floor of one replica, `minAvailable: 1` would block
node drains forever, hanging cluster upgrades on a budget that can never be
satisfied.

### Two Deployments, one image

Per ADR-1, `deployment.yaml` runs `args: ["server"]` and
`worker-deployment.yaml` runs `args: ["worker"]` from the same image.

They are distinguished by an `app.kubernetes.io/component` label (`server` or
`worker`), and the Service, PodDisruptionBudget, and topology-spread selectors
all pin `component: server`. That pinning is load-bearing: without it the
Service would also select worker pods, which run no HTTP listener, and a share
of every request would land on a pod that cannot answer it.

The worker carries no probes — it has no listener, and distroless has no shell
for an `exec` probe — and no HPA, because worker capacity should track queue
depth (via KEDA or a custom metric) rather than CPU.

> **Caveat:** `spec.selector` is immutable in Kubernetes. Adding the component
> label to an already-deployed Deployment requires deleting and recreating it,
> not a rolling update.

### Probes and scaling

Liveness is `GET /healthz` (process alive; restarts only on hang or crash) and
readiness is `GET /readyz` (traffic gate; 503 during startup and drain). Because
the image is distroless with no shell, the container-level Docker healthcheck
uses the binary itself: `app healthcheck`.

Scaling is HPA-owned at 70% CPU — the base allows 1–10 replicas, and the prod
overlay widens the ceiling to 20 while keeping the floor at 1.

> **The floor is 1 for a correctness reason, not a cost one.** The HTTP and
> worker processes hold no state, but the reference `Storer`
> (`adapters.Database`) is an in-memory map — one per pod, shared with nobody.
> Running a second replica against it splits your data silently: an item created
> on one pod simply will not exist on another, with nothing in the logs to
> explain it. Swap `Storer` for a real shared database (see
> [Dependency injection](#3-dependency-injection)) **before** raising
> `minReplicas`.

---

## 9. GitOps and configuration

Configuration is read exclusively from the environment (12-factor), which is
what allows the deployment to be declarative and git-managed rather than
imperative:

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

### How a config change reaches running pods

Changing production config is a PR against the `configMapGenerator` literals in
`overlays/prod/kustomization.yaml`. The content hash in the generated ConfigMap
name changes, which changes the Deployment's reference to it, which makes
Kubernetes perform a rolling update. That chain is why a committed config change
actually reaches running pods **with no operator involved** — a plain,
unhashed ConfigMap would update in the cluster while pods kept serving the old
values until someone restarted them by hand.

Secrets never enter git in plaintext; only references do, resolved at reconcile
time from your backing store. You can swap External Secrets Operator for SOPS or
sealed-secrets freely, since the Deployment depends only on the resulting Secret
*name*. Note that secret **rotation** does not auto-roll pods — the hash trick
covers ConfigMaps only — so add Stakater Reloader if you need that.

The Go code is unchanged across all of this. It just reads environment
variables. See [ops/README.md](ops/README.md) for the app-repo-versus-config-repo
split at scale.

### Fail-secure startup

`internal/config` validates cross-field rules — the ones no single key can
express — in `Config.validate`.

The rule that ships is the authentication guard: **`APP_ENV=prod` combined with
`AUTH_ENABLED=false` refuses to start.** An unauthenticated API should never
reach production merely by inheriting a default, so the process fails loudly
instead. The base ConfigMap therefore sets `AUTH_ENABLED=true` explicitly, with
the keys arriving from the Secret; a pod with auth enabled but no
`AUTH_API_KEYS` also fails fast rather than serving open.

Services whose authentication genuinely lives in an API gateway or service mesh
opt out deliberately with `AUTH_ALLOW_INSECURE_NO_AUTH=true`. The variable is
named that way on purpose: disabling authentication in production should be a
typed, reviewable decision in git, not an oversight.

---

## 10. Architecture decisions (ADRs)

Source comments across the codebase cite these by number — `ADR-6` is the most
frequent. Each records what was chosen, what was rejected, and the consequence
you inherit. Read the relevant one before arguing with the code it explains.

**ADR-1 — Single binary, cobra subcommands.** One image runs `server` or
`worker`, selected by Kubernetes `args`. *Rejected*: separate binaries per
process (two images, two build paths, inevitable drift). *Consequence*: the
worker image is byte-identical to the server image, so there is one artifact to
scan, sign, and promote; the composition roots share `internal/cli/bootstrap.go`.

**ADR-2 — Constructor DI, no framework.** All wiring is explicit in
`internal/cli/server.go` and `worker.go`; dependencies flow in as interfaces.
*Rejected*: `google/wire` (codegen opacity), `uber/fx` (runtime magic, global
lifecycle). *Consequence*: wiring is roughly 40 lines you can read top to bottom;
adding a dependency is one constructor argument; every unit is testable with a
hand-written fake and no container.

**ADR-3 — Domain-owned ports plus in-memory defaults.** The domain declares
`Storer` and `EventPublisher` (`internal/item/ports.go`); adapters satisfy them.
*Rejected*: a shared `pkg/repository` generic interface, which leaks storage
concerns into the domain. *Consequence*: the template runs with zero
infrastructure and real adapters are purely additive — but the shipped `Storer`
is an in-memory map, which is why `minReplicas` is 1 (see
[Kubernetes lifecycle](#8-kubernetes-lifecycle)).

**ADR-4 — stdlib `log/slog` over zap.** Trace correlation is added by a custom
handler (`internal/logger/trace_handler.go`). *Rejected*: zap. *Consequence*:
zero logging dependency; `slog` has been stdlib since Go 1.21 and is fast enough
at this template's baseline.

**ADR-5 — Hand-rolled resilience blueprint.** `pkg/resilience` is roughly 200
lines of context-aware retry and circuit breaker, dependency-free and fully
tested. *Rejected*: `cenkalti/backoff` and `sony/gobreaker` — both fine
libraries, but the template prefers a readable blueprint you can keep, replace,
or delete. *Consequence*: you own the code; swap in a library the moment your
needs outgrow it.

**ADR-6 — Errors-only logging; metrics carry everything else.** `slog` is used
at `error` level only on production paths; throughput, latency, and state
signals are OTel metrics (see [Observability](#6-observability)). *Rejected*:
info-level request logging, which is expensive at volume and duplicates what
metrics do better. *Consequence*: `LOG_LEVEL` defaults to `error`, and
success-path observability comes from metrics and traces rather than logs.

---

## Extending the template

See [CLAUDE.md](CLAUDE.md) for the step-by-step recipes: adding a domain,
adding or removing a storage or bus adapter, detaching the worker or the HTTP
server, and the quality gates every change must clear.
