# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Commands

> **json/v2:** the code uses `encoding/json/v2`, which is stable as of Go 1.27
> and needs no build flag. It does require the `go` directive in `go.mod` to be
> `1.27.0` or later — under an older directive the compiler rejects
> `json.UnmarshalRead` with "requires go1.27 or later", which is why the
> directive and the `toolchain` line move together.

| Task | Command |
|------|---------|
| Build binary | `make build` (ldflags inject version/commit/build time) |
| Run HTTP server | `make run` (or `go run ./cmd server`) |
| Run worker | `make run-worker` |
| All tests (race) | `make test` |
| Coverage gate (≥85%) | `make cover` — measured over `./cmd/... ./internal/... ./pkg/...` |
| Lint | `make lint` (gofmt + go vet + golangci-lint; `make tools` installs) |
| Vulnerability scan | `make vuln` |
| OpenAPI lint | `make spec-lint` |
| Docker image | `make docker-build` |
| Local stack (app + worker + OTel collector) | `make compose-up` / `make compose-down` |
| K8s render / deploy | `make k8s-render` / `make k8s-apply` |

Run a single test: `go test -race -run TestName ./internal/item/`.

## Architecture (read ARCHITECTURE.md for the full guide)

Layout is the standard Go community layout — `cmd/` (thin entrypoint),
`internal/` (private application code, organized **by domain**: one dir per
domain holds its entity, ports, adapters, and HTTP handler together), and
`pkg/` (only the zero-dependency packages, genuinely reusable as a library).
No `*bus`/`*app`/`*db` suffix convention — packages are disambiguated by
import path instead (`internal/item` vs `internal/item/http` vs
`internal/item/adapters`).

- **Composition root**: `internal/cli/server.go` (`runServer`) and `.../worker.go` (`runWorker`) — the ONLY places concrete types are constructed. Everything else receives interfaces via constructors. Path: `main` → `cli.Execute` → cobra → `runServer`/`runWorker`. Both share one bootstrap helper (`internal/cli/bootstrap.go`) for the logger + telemetry singletons — pure construction dedup, no shared lifecycle (server and worker are always separate processes).
- **Domain owns its contracts**: `internal/item` defines `Storer` and `EventPublisher` in `ports.go`; `internal/item/adapters` (`Database`, `CacheStore`, `Publisher`) satisfies them. The domain imports no gin/http/broker; the HTTP adapter is `internal/item/http`.
- **Infrastructure kernel** (`internal/web`, `internal/web/client`, `internal/cache`, `internal/messaging`, `internal/logger`, `internal/telemetry`, `internal/lifecycle`, `internal/worker`): template-specific, domain-agnostic; imports nothing above it.
- **Generic libraries** (`pkg/uid`, `pkg/resilience`): zero dependencies on anything in this module — the only packages safe to import from outside it.
- **Middleware** lives in `internal/middleware`; the composition root assembles the chain and passes it to `internal/web` via `WithGroupMiddleware`. Order is load-bearing and set in one place (`runServer`): `RequestID` → `SecurityHeaders` → `Telemetry` → `Recovery` → `Throttle` → `otelgin` → `Auth` → `Timeout` → `BodyLimit`. Telemetry sits high so it observes 401/429/503 and recovered 500s below it; `Timeout` sits after auth/throttle so rejected requests are not charged against the handler deadline, and before `BodyLimit` so the deadline also covers request decoding.
- **API contract**: `api-spec.yaml` (OpenAPI 3.1) is the source of truth for HTTP. Change the spec first, lint it, then change code.

## Before Making Any Change

Read in this order before writing code — do not skip to editing:

1. **ARCHITECTURE.md** — the full dependency diagram, DI pattern, startup flow, lifecycle. The Architecture section above is a summary, not a substitute.
2. **The closest existing analog.** `internal/item` is the reference implementation of the package-by-domain pattern — every new domain should be structurally identical to it: same file names, same shape, same order of construction in the composition root. Don't invent a new shape for a problem this repo already solved once.
3. **"Where New Code Goes"** (below) — a decision table for the changes that come up most: new domain, new entity, new endpoint, new adapter, new infra package, new shared type. Match your change to a row before creating a file or folder.
4. **"Pluggable Boundaries" / "How to Detach a Part"** (below) — know whether what you're about to touch is optional infrastructure (messaging, cache, worker, auth, the whole HTTP server — each can be deleted cleanly per those sections) or load-bearing (the composition root in `internal/cli`, `internal/config`, `cmd/main.go`, and whichever domain you're actively working on). Optional parts tolerate a rough edit; load-bearing parts don't.

**Hard rule**: never resurrect `api/`, `app/`, `business/`, or `foundation/` as directory names, and never split one domain's entity/ports/adapters/HTTP across separate top-level directories again. That Ardan Labs-style layering is exactly what this template was restructured away from — reintroducing it (even partially, even for "just one new domain") undoes the reason `internal/item` looks the way it does. If a change would recreate that split, stop and re-read the Architecture section instead of proceeding.

## Where New Code Goes

Decision table for the most common additions — match your change to a row and
put it exactly there:

| Adding… | Goes in | Why |
|---|---|---|
| A new domain (its own aggregate, lifecycle, storage) | `internal/<name>/` — copy `internal/item`'s shape exactly | package-by-domain: one directory owns everything about one domain |
| A new entity that belongs to an EXISTING domain's aggregate (no independent lifecycle/storage of its own) | `internal/<domain>/<entity>.go`, same package as the domain | it's not a new bounded context — don't fragment the domain package over it |
| A new HTTP endpoint on an EXISTING domain | `internal/<domain>/http/handler.go` (new method + route in `Register`); extend `api-spec.yaml` first, then `make spec-lint` | the domain's HTTP surface stays colocated with the domain, never in a separate top-level tree |
| A new endpoint spanning multiple domains (BFF-style aggregation, orchestration) | a new package under `internal/`, named for the use case — never `common`/`shared` | keeps orchestration out of any single domain's adapter; wire it at the composition root like any other handler |
| A new storage/cache/broker adapter for an existing domain | new file in `internal/<domain>/adapters/`, named for the role (`redis.go`, `postgres.go`), implementing the port declared in that domain's `ports.go` | ports live in the domain; adapters satisfy them, never the reverse — see "Domain owns its contracts" above |
| A new middleware | `internal/middleware/` | one chain, assembled once at the composition root |
| A new CLI command | `internal/cli/<name>.go`, wire `newXCmd()` into `root.go` | the composition root is the only place commands are assembled |
| A new infra boundary specific to this template (new cache backend, new messaging driver) | extend the existing package's factory with a new `case` (e.g. add to `internal/cache`), not a new top-level package | one boundary = one package with a driver switch, not one package per driver |
| A genuinely generic utility with ZERO dependency on anything under `internal/` | `pkg/<name>/` | `pkg/` is what's safe to import from outside this module — anything importing `internal/` doesn't belong there |
| A type/DTO you want to share across multiple domains | don't — duplicate the small struct instead | a shared `common`/`shared` package re-couples domains that package-by-domain deliberately decoupled |

### Adding a new domain (`<name>`), step by step

1. **Domain**: create `internal/<name>/` (package `<name>`): `<name>.go` (entity + invariants), `service.go` (`Service` with `NewService(log, store, pub)`), `ports.go` (`Storer`, `EventPublisher`), `errors.go` (sentinel errors `ErrNotFound`, `ErrInvalidArgument`), `events.go` (domain event type). Copy `internal/item`.
2. Write the domain tests first (table-driven, hand-written fakes) — RED before implementation.
3. **Adapters**: `internal/<name>/adapters/` implements the ports — `database.go` (in-memory or real DB, parameterized queries only, named for the role not the implementation — e.g. `Database`, not `Memory`), and optionally `cache.go` (cache decorator) and `events.go` (messaging adapter). Compile-check each (`var _ <name>.Storer = …`).
4. **HTTP**: extend `api-spec.yaml` (then `make spec-lint`); add `internal/<name>/http/` (package `http`): a `Handler` with `Register(rg)`, request/response models, and domain→HTTP error mapping (`web.Error`, `web.Code*`). Import it under an alias (e.g. `<name>http`) wherever `net/http` is also imported in the same file.
5. **Wire** in `internal/cli/server.go`: construct store → (cache) → publisher → service → `<name>http.NewHandler(svc)` → add to `web.WithRoutes(...)`. One constructor call per dependency.
6. `make test cover lint` — all green before done.

## Code Style (Ardan Labs + Uber Go + this repo's rules)

- Errors: `fmt.Errorf("doing X: %w", err)` — never bare `return err`; inspect with `errors.Is`/`errors.As`, never string matching.
- **Never discard an error.** `_ =` and `_, _ =` are banned outright — there is no "unactionable cleanup" exception. Cleanup on a failing path joins its error instead of dropping it: `errors.Join(primaryErr, closeErr)` (Join skips nils, so the success path stays clean) — see `internal/cache/redis.go` and `internal/web/client/client.go`. For deferred closes, assign to a named return, as in `internal/cli/healthcheck.go`.
- **No global variables.** The only package-level `var` allowed are sentinel errors (`var ErrNotFound = errors.New(...)` — Go has no error constants) and blank compile-time assertions (`var _ Iface = (*T)(nil)`); neither is mutable state. The sole exception is build metadata in `internal/cli/version.go`, because `-ldflags -X` can only write into a package-level var — it is write-once at link time and never mutated at runtime.
- `ctx context.Context` is always the first parameter, named `ctx`; propagate it through every layer.
- Constructor DI only: `func NewX(deps...) *X`. No globals, no `init()` logic, no service locators.
- Interfaces live in the CONSUMER package; single-method interfaces end in `-er`; add `var _ Iface = (*Impl)(nil)` compile checks in implementations.
- Table-driven tests with testify; every error path tested; `go test -race` must pass.
- Concrete types or generics. `any` is allowed ONLY as a generic constraint (`Retry[T any]`, `web.BindJSON[T any]`) — never as a value/parameter type (`interface{}` says nothing). The lone exception is a signature a third-party library dictates, e.g. gin's recovery callback `func(c *gin.Context, err any)`. No reflection.
- Package names: lowercase, single word. No `utils`/`helpers`/`common`.
- **Logging (ADR-6, non-negotiable)**: production paths log at `error` level ONLY, always with `request_id`/`trace_id` and never PII/secrets/payloads. Every non-error signal (counts, durations, throughput) is an OTel metric via `internal/telemetry`. Do not add info/warn logs to request or message paths.
- **JSON**: use `encoding/json/v2` (imported as `jsonv2`); on HTTP boundaries decode via `web.BindJSON` (`jsonv2.UnmarshalRead` + `RejectUnknownMembers`). No v1 `encoding/json`.
- Comments explain *why*, never *what*. No commented-out code.

## Pluggable Boundaries (config-driven factories)

Switching or removing an adapter is a one-package + one-line change — never a churn through call sites:

- **Messaging** — `internal/messaging.New(driver)` returns `Publisher, Consumer, io.Closer` by `MESSAGING_DRIVER` (`memory` | `none`).
- **Cache** — `internal/cache.New(ctx, cfg)` returns a `Cache` by `CACHE_DRIVER` (`none` | `memory` | `redis`); the composition root wraps the store with `adapters.NewCache` when enabled. It runs *alongside* the DB, never replaces it.
- **Storage** — the store is built directly (`adapters.NewDatabase()`); swap it for a real DB by adding a new adapter in `internal/item/adapters` and changing one constructor call at the composition root (no factory ceremony for a single driver).
- **Telemetry** — no driver: OTLP is vendor-neutral. Point `OTEL_EXPORTER_OTLP_ENDPOINT` at any collector.

### How to Detach a Part (remove cleanly, no dangling code)

- **Messaging**: set `MESSAGING_DRIVER=none` (domain gets a no-op publisher, worker idles). To delete entirely: remove the `worker` command, `internal/worker`, `internal/item/adapters/events.go`, `internal/messaging`, and the messaging lines in `server.go`.
- **Cache**: set `CACHE_DRIVER=none` (the composition root skips the decorator entirely — zero overhead). To delete entirely: remove `internal/cache`, `internal/item/adapters/cache.go`, the `CacheConfig` block in `internal/config`, and the cache lines + lifecycle component in `server.go`. The store is untouched — the cache only ever decorated it.
- **Auth**: set `AUTH_ENABLED=false` (dev) or keep the guard satisfied with `AUTH_ALLOW_INSECURE_NO_AUTH=true`. To delete entirely: remove `internal/auth`, `internal/middleware/auth.go`, the `AuthConfig` block, and the auth lines in `server.go`.
- **Outbound HTTP client**: nothing imports `internal/web/client` until you construct one, so deleting the package is a no-op for the rest of the tree.
- **Worker**: delete `internal/worker` + `newWorkerCmd()` + `worker.go`. The server is independent.
- **HTTP server**: delete `internal/web` + `internal/item/http` + `internal/middleware` + `newServerCmd()`; keep `worker` for a consumer-only service.

## Configuration

All config is env-based with validated defaults — see `internal/config/config.go` for the full key table (`HTTP_PORT`, `HTTP_*_TIMEOUT` including `HTTP_HANDLER_TIMEOUT`, `SHUTDOWN_TIMEOUT`, `LOG_LEVEL`, `MESSAGING_DRIVER`, `CACHE_*`, `AUTH_*`, `THROTTLE_*`, `OTEL_*`, …). `Load` uses the "errors are values" accumulator pattern (one parser, one error check) — add a field as one line in the struct literal. Fail-fast on invalid values. Cross-field rules (ones no single parser can see) go in `Config.validate`, which is where the prod auth guard lives: `APP_ENV=prod` with `AUTH_ENABLED=false` refuses to start unless `AUTH_ALLOW_INSECURE_NO_AUTH=true` is set deliberately.

Never add a config key that nothing reads. Config parsed but never consumed misrepresents what the service does — add the key in the same change that adds its call site.

Local dev: `cp .env.example .env` and edit. **Never commit `.env`/`.envrc`** — they're gitignored, and `make hooks` installs a pre-commit hook that blocks them (only `.env.example`, non-secret, is committable). Never read or write `.env` files from code; secrets come from the environment (K8s Secret via External Secrets Operator), never ConfigMaps or source.

## GitOps Deployment

The app is 12-factor (config from env only), so config is managed declaratively in git, not imperatively:

- **`ops/k8s/base/`** — environment-agnostic manifests: two Deployments from one image (`deployment.yaml` = `server`, `worker-deployment.yaml` = `worker`, split by an `app.kubernetes.io/component` label the Service/PDB selectors pin), plus `service.yaml`, `hpa.yaml`, and `pdb.yaml`.
- **`ops/k8s/base`** uses a **`configMapGenerator`** (hashed name) so a committed config change rolls the Deployment automatically — no operator. Overlays override keys with `behavior: merge`.
- **`ops/k8s/overlays/{dev,staging,prod}/`** patch env, image tag, namespace, scaling per environment. `make k8s-render ENV=prod` / `make k8s-apply ENV=prod`.
- **Secrets** — `overlays/prod/external-secret.yaml`: only *references* live in git; External Secrets Operator materializes the Secret (swap for SOPS/sealed-secrets). Secret *rotation* auto-roll needs Stakater Reloader — see `ops/README.md`.
- **`ops/argocd/application.yaml`** — optional ArgoCD Application; repo is the source of truth, changes ship by PR (Flux works the same).
- **Repo strategy**: `ops/` is a runnable reference. At scale, extract `overlays/` + `argocd/` to a separate GitOps repo, keep `base/` here — see `ops/README.md`.

Change prod config = edit the `configMapGenerator` literals in `overlays/prod/kustomization.yaml` + merge. New hash → rolling update. No `kubectl` by hand.

## Quality Gates (all must pass before any handoff)

`gofmt` clean · `go vet` clean · `golangci-lint run` 0 errors · `go test -race ./...` green · coverage ≥85% (`make cover`) · `govulncheck` clean · `make spec-lint` 0 errors when touching HTTP.

Lint rules live in `.golangci.yml` — it enables the linters that enforce this
file's stated rules (`gosec`, `errorlint`, `nilerr`, `noctx`, `bodyclose`,
`revive`, `gocritic`, …), which the default linter set does NOT cover. Tool
versions are pinned in the Makefile and must match `.github/workflows/ci.yml`.

Run `make tools` once to install `golangci-lint` + `govulncheck` (not vendored). `go.mod` pins `toolchain go1.27.1` — the `go` command auto-downloads it. `encoding/json/v2` is stable from Go 1.27 and needs no build flag, but it does require the `go` directive to be `1.27.0`+.

CI (`.github/workflows/ci.yml`) enforces all of this on every push/PR — a **secret guard** (fails on any committed `.env`/`.envrc`), the Go gates (fmt, vet, golangci-lint, `-race` + ≥85% coverage, govulncheck), and Spectral spec lint. The guard is server-side, so it holds even if a contributor skipped `make hooks`.
