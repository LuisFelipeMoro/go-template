# go-template

Production-ready Go 1.26 microservice template — standard Go layout (`cmd`/`internal`/`pkg`) with package-by-domain organization, explicit dependency injection, gin HTTP + async worker, OpenTelemetry, graceful shutdown, Docker & Kubernetes native.

> 🇧🇷 [Versão em português abaixo](#go-template-português)

## Contents

New here? Run the [Quickstart](#quickstart), then read
[How this template works](#how-this-template-works) — those two together take
about ten minutes and cover most of what you need.

| Section | Read it when you want to… |
|---|---|
| [Features](#features) | see what you get out of the box |
| [Quickstart](#quickstart) | get it running locally right now |
| [Docker & Compose](#docker--compose) | run the full local stack with telemetry |
| [Kubernetes & GitOps](#kubernetes--gitops) | deploy it, and understand the scaling and auth caveats |
| [Pluggable & detachable](#pluggable--detachable) | remove a part you don't need |
| [What this template leaves to you](#what-this-template-deliberately-leaves-to-you) | see every seam and what plugs into it (with a Kafka example) |
| [Configuration](#configuration) | look up an environment variable |
| [Project structure](#project-structure) | find out which directory owns what |
| [How this template works](#how-this-template-works) | understand the three ideas behind the layout |
| [Navigating the code](#navigating-the-code) | trace a request from `main()` to a handler |
| [Renaming the module](#renaming-the-module) | start your own service from this |

For the reasoning behind the design — the dependency rule, the shutdown
sequence, the ADRs — see **[ARCHITECTURE.md](ARCHITECTURE.md)**. For where to
put new code, see **[CLAUDE.md](CLAUDE.md)**.

## Features

The short version: this is a service skeleton you can deploy on day one and
still recognize a year later. Everything below is wired, tested, and documented
— the parts you don't need can be deleted cleanly (see
[Pluggable & detachable](#pluggable--detachable)).

- **Single binary, multiple entrypoints** — `spf13/cobra` subcommands: `server` (HTTP), `worker` (async consumer), `version`, `healthcheck`.
- **Package-by-domain + explicit DI** — domain owns its interfaces (`Storer`, `EventPublisher`); all wiring lives in one composition root; no DI framework, no globals.
- **Agnostic infrastructure** — in-memory storage and message bus included; swap in Postgres/SQS/SNS/Kafka by implementing one interface, changing one line.
- **Observability** — OpenTelemetry traces + metrics over OTLP (Datadog/Jaeger-ready); structured `slog` JSON logs with automatic `trace_id` correlation. Errors-only logging policy: non-error signals are metrics.
- **Resiliency** — context-aware retry (exponential backoff + jitter) and circuit breaker blueprints; per-request handler deadlines that actually cancel downstream work; graceful shutdown draining in-flight work on SIGTERM.
- **Secure by default** — bearer auth that *fails closed* in prod, constant-time key comparison, strict JSON decoding, security headers, request body limits, distroless non-root image, and a `.golangci.yml` running `gosec`/`errorlint`/`bodyclose` in CI.
- **Contract-first HTTP** — `api-spec.yaml` (OpenAPI 3.1) with Spectral linting; sample CRUD domain at `/v1/items`; `/healthz` + `/readyz` probes.
- **Ship-ready packaging** — multi-stage Dockerfile (distroless, non-root, static), docker-compose with an OTel collector, and a Kustomize base wired for zero-downtime rollouts: server + worker Deployments, Service, HPA, PodDisruptionBudget, `preStop` drain hook, topology spread, and a hashed ConfigMap.

## Prerequisites

- Go 1.26+
- make
- Docker (+ Compose v2) — for the local stack
- kubectl (with kustomize) — for deploys
- `make tools` installs golangci-lint and govulncheck

## Quickstart

```bash
git clone https://github.com/luisfelipecoelho/go-template my-service
cd my-service
make run                        # HTTP server on :8080
```

```bash
curl localhost:8080/healthz
curl -X POST localhost:8080/v1/items \
  -H 'Content-Type: application/json' \
  -d '{"name":"widget","quantity":3,"price_cents":1990}'
curl localhost:8080/v1/items
```

Tests, lint, coverage (≥85% enforced):

```bash
make test lint cover
```

## Docker & Compose

The image is multi-stage and ends on distroless static: no shell, no package
manager, non-root by default. Compose brings up the server, the worker, and an
OpenTelemetry collector together, which is the fastest way to see traces and
metrics actually flowing.

```bash
make docker-build               # distroless image, non-root, static binary
make compose-up                 # server + worker + OTel collector
docker compose logs otel-collector   # see exported traces/metrics
make compose-down
```

## Kubernetes & GitOps

```bash
make k8s-render ENV=dev         # render an overlay (dev|staging|prod)
make k8s-apply  ENV=prod        # apply base + per-env patches
```

Kustomize `base` + `overlays/{dev,staging,prod}` keep config declarative in git. The base uses a **`configMapGenerator`** (hashed name), so a committed config change rewrites the Deployment's ConfigMap reference and triggers a rolling update — the new value reaches running pods with no operator. Secrets stay out of git: `overlays/prod/external-secret.yaml` references your store via External Secrets Operator (swap for SOPS/sealed-secrets). `ops/argocd/application.yaml` makes the repo the source of truth (Flux works the same). See **[ops/README.md](ops/README.md)** for the full GitOps flow, secret rotation, and the app-repo-vs-config-repo strategy at scale. Liveness maps to `/healthz`, readiness to `/readyz`; on SIGTERM the pod flips readiness, drains in-flight requests, flushes telemetry, and exits before the 30s grace period.

The base ships two Deployments from the one image (ADR-1): `go-template` (`args: ["server"]`, fronted by the Service, scaled by the HPA) and `go-template-worker` (`args: ["worker"]`, no Service, no probes — it runs no listener). They are separated by an `app.kubernetes.io/component` label so the Service can never route HTTP traffic to a worker pod. Note the worker idles while `MESSAGING_DRIVER=memory`, because that bus is in-process — it earns its keep once you point the driver at a real broker.

> **Scaling caveat**: the base HPA defaults to `minReplicas: 1` on purpose, and the prod overlay keeps it there. The reference `Storer` (`adapters.Database`) is an in-memory map, one per pod — running more than one replica against it silently splits your data across pods (an item created on one pod 404s on another, with nothing in the logs to explain it). Swap `Storer` for a real shared database before raising `minReplicas` past 1; `maxReplicas` is already 20, so the HPA can still absorb load above that floor.

> **Auth is fail-secure in prod**: `APP_ENV=prod` with `AUTH_ENABLED=false` makes the process refuse to start, so an unauthenticated API can never be reached by inheriting a default. The base sets `AUTH_ENABLED=true`, keys arrive from the Secret (`overlays/prod/external-secret.yaml`), and a pod with auth on but no `AUTH_API_KEYS` fails fast rather than serving open. If authentication genuinely lives in your gateway or service mesh, opt out explicitly with `AUTH_ALLOW_INSECURE_NO_AUTH=true`.

## Pluggable & Detachable

Every infrastructure boundary is chosen by a config-driven factory, so switching or removing a part is one env var + one package:

- **Messaging** (`MESSAGING_DRIVER`) resolves through `internal/messaging.New`. Add an adapter = new package + one `case`; remove one = delete the package + its case. The interface and call sites never change.
- **Cache** (`CACHE_DRIVER`) resolves through `internal/cache.New` and decorates the store via `adapters.NewCache` — it runs *alongside* the database, never replacing it (`memory` + `redis` both real).
- **Detach messaging** with `MESSAGING_DRIVER=none` (no-op publisher/consumer). **Detach** the worker, HTTP server, or outbound client by deleting the package — nothing else breaks.
- **Telemetry** is vendor-neutral OTLP: point `OTEL_EXPORTER_OTLP_ENDPOINT` at Datadog, Jaeger, Tempo, Grafana — no code change.
- **Outbound calls**: `internal/web/client` — resilient (retry + circuit breaker + OTel) client for BFF/ingestion.

## What This Template Deliberately Leaves to You

This is a **starting point**, not a finished service. Everything below is an
interface with a runnable default behind it — the template ships the *seam*, you
plug in what your app actually needs. Nothing here is missing by accident.

| Seam (the interface) | Ships with | You plug in | Where |
|---|---|---|---|
| `item.Storer` | in-memory map | Postgres, DynamoDB, … | new file in `internal/item/adapters/` + one line in the composition root |
| `messaging.Publisher` / `Consumer` | in-memory bus, `none` | Kafka, RabbitMQ, SQS/SNS, NATS | implement 2 methods + one `case` in `internal/messaging/factory.go` |
| `middleware.Authenticator` | static API keys | JWT, OIDC, mTLS | new type in `internal/auth/` + one line in the chain |
| `cache.Cache` | `none` / `memory` / `redis` | already real — Redis is production-ready | env vars only |
| `middleware.Limiter` | in-process semaphore | distributed limiter | implement `Acquire`/`Release` |
| Ingress / Gateway | — | your controller (nginx, ALB, Gateway API) | add a manifest to `ops/k8s/` |
| Alerts & SLOs | metrics are emitted | your `PrometheusRule` / dashboards | your monitoring stack |
| Authorization (tenancy, ownership) | — | your model — the template does authn only | `internal/item` + handlers |

The rule the template follows: **no broker SDK type, no driver type, and no
cloud SDK type may appear in an interface.** That is what keeps every one of
these swappable without touching the domain, the worker, or any call site.

### Example: plugging in Kafka

Three touch points, none of them in your domain:

```go
// 1. internal/messaging/kafka.go — satisfy the two interfaces.
//    No kafka types in the signatures; Message/Handler are the contract.
type Kafka struct{ /* your client */ }

func (k *Kafka) Publish(ctx context.Context, msg messaging.Message) error { … }
func (k *Kafka) Consume(ctx context.Context, topic string, h messaging.Handler) error { … }
func (k *Kafka) Close() error { … }
```

```go
// 2. internal/messaging/factory.go — one case.
case "kafka":
    k := NewKafka(...)
    return k, k, k, nil
```

```bash
# 3. Set the driver. No other code changes — the domain publishes through
#    item.EventPublisher and never learns a broker exists.
MESSAGING_DRIVER=kafka
```

Removing it is the same in reverse: delete the file and the `case`. To detach
messaging entirely, set `MESSAGING_DRIVER=none` (the domain gets a no-op
publisher) or delete the package per *Pluggable & Detachable* above.

## Configuration

Every knob is an environment variable — there is no config file, and there
should never be one. Values are validated at startup and the process refuses to
run on anything unparsable or out of range, so a typo fails immediately and
loudly rather than halfway through the first request.

Locally, `cp .env.example .env` and edit; in Kubernetes the same keys arrive
from a ConfigMap, with secrets from a Secret. The two rows worth reading before
you deploy anything are `AUTH_ENABLED` and `CACHE_REDIS_TLS`.

| Variable | Default | Description |
|----------|---------|-------------|
| `APP_ENV` | `dev` | `dev` or `prod` (gin mode) |
| `HTTP_PORT` | `8080` | HTTP listen port |
| `HTTP_READ_TIMEOUT` | `5s` | server read timeout |
| `HTTP_WRITE_TIMEOUT` | `10s` | server write timeout |
| `HTTP_IDLE_TIMEOUT` | `60s` | keep-alive idle timeout |
| `HTTP_HANDLER_TIMEOUT` | `8s` | per-request deadline on the handler context (cancels downstream work → 504); keep below `HTTP_WRITE_TIMEOUT` |
| `SHUTDOWN_TIMEOUT` | `20s` | graceful-shutdown bound |
| `LOG_LEVEL` | `error` | errors-only in prod; use `debug` locally |
| `MAX_BODY_BYTES` | `1048576` | request body limit |
| `MESSAGING_DRIVER` | `memory` | messaging adapter: `memory` or `none` (detach) |
| `CACHE_DRIVER` | `none` | read-through cache: `none` \| `memory` \| `redis` (runs alongside the store) |
| `CACHE_TTL` | `5m` | cache entry lifetime |
| `CACHE_REDIS_ADDR` | `localhost:6379` | Redis-compatible address (Redis/Valkey/KeyDB/Dragonfly) |
| `CACHE_REDIS_TLS` | `false` | encrypt Redis traffic — **set `true` for any managed/remote Redis**, else the password and cached values cross the network in plaintext |
| `CACHE_REDIS_DB` | `0` | Redis logical database index (0..15) |
| `CACHE_REDIS_PASSWORD` | — | Redis auth password (**secret**; from env/Secret, never committed) |
| `AUTH_ENABLED` | `false` | enforce a bearer token on `/v1` (**off by default — turn it on before exposing the service**) |
| `AUTH_API_KEYS` | — | comma-separated bearer keys (**secret**; required when auth on) |
| `THROTTLE_ENABLED` | `false` | per-instance in-flight concurrency cap (backpressure, not a rate limit) |
| `THROTTLE_MAX_INFLIGHT` | `256` | max concurrent `/v1` requests when throttling |
| `AUTH_ALLOW_INSECURE_NO_AUTH` | `false` | opt out of the prod auth guard — only when a gateway/mesh enforces authn |
| `OTEL_ENABLED` | `false` | enable traces + metrics export |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | OTLP gRPC endpoint |
| `OTEL_SERVICE_NAME` | `go-template` | service resource name |
| `OTEL_SAMPLE_RATIO` | `1.0` | trace sampling ratio 0..1 |

## Project Structure

The layout follows the standard Go community convention: `cmd/` (thin
entrypoint), `internal/` (private application code, organized **by domain** —
one directory per domain holds its entity, ports, adapters, and HTTP handler
together), and `pkg/` (only the zero-dependency packages, genuinely reusable
as a library). No `*bus`/`*app`/`*db` suffix convention — packages are
disambiguated by import path instead.

```
cmd/
  main.go                              delegates to cobra
internal/
  cli/                                 cobra cmds + composition roots (server, worker) + shared bootstrap
  item/                                domain: entity, invariants, Service, ports (no gin/http)
    adapters/                          Database (in-memory Storer), CacheStore (decorator), Publisher (events)
    http/                              item HTTP adapter: handler, request/response models, error mapping
  config/                              typed env config
  auth/                                bearer authenticator (StaticKeys)
  middleware/                          gin middleware (RequestID, Telemetry, Recovery, Auth, Throttle, …)
  web/                                 gin server, health, error envelope, BindJSON  (+ web/client)
  messaging/ cache/ logger/ telemetry/ lifecycle/ worker/
pkg/
  uid/ resilience/                     zero-dependency generic libraries
ops/                                  docker, k8s (Kustomize), compose, otel-collector
```

`internal/item` (the domain) imports no gin/http/broker — the adapters
(`internal/item/adapters`, `internal/item/http`) depend on it, never the
reverse.

## How This Template Works

Three ideas make this codebase predictable to extend:

1. **Package-by-domain, not package-by-layer.** Everything about the `item`
   domain — its entity, business rules, ports, storage/cache/messaging
   adapters, and HTTP handler — lives under one directory tree,
   `internal/item/`. Nothing about `item` lives anywhere else. Adding a new
   domain means copying that one tree, not touching four different
   directories.
2. **The domain owns its contracts.** `internal/item/ports.go` declares
   `Storer` and `EventPublisher`; `internal/item/adapters/` and
   `internal/item/http/` depend on the domain, never the other way around.
   This is what makes storage/broker swaps a one-file change (see *Pluggable
   & Detachable* above) instead of a refactor.
3. **One composition root.** `internal/cli/server.go` and `.../worker.go` are
   the only places concrete types get constructed and wired together.
   Everything else receives its dependencies as interfaces through a
   constructor — no globals, no service locator, no hidden wiring.

Most infrastructure is optional by design (messaging, cache, auth, the whole
HTTP server, even the worker) and can be deleted without touching the rest —
see *Pluggable & Detachable* above. What's load-bearing is the composition
root, config, and whichever domain you're actively extending.

**Using an AI coding assistant on this repo?** [CLAUDE.md](CLAUDE.md) has a
structured decision table for where new code goes (new domain, new entity,
new endpoint, new adapter, ...) and what to read before making structural
changes — point it there first.

## Navigating the Code

Two independent apps ship in one binary, selected by a cobra subcommand — deploy
and scale them separately (server pods vs worker pods).

**HTTP server** — `app server`:

```
cmd/main.go                → cli.Execute()                         (internal/cli/root.go, cobra)
  └─ newServerCmd → runServer(ctx, cfg)   internal/cli/server.go    ← composition root (explicit DI)
       adapters.NewDatabase()                 internal/item/adapters   (+ adapters.NewCache if CACHE_DRIVER)
       adapters.NewPublisher(messaging.New())  internal/item/adapters
       item.NewService(store, publisher)      internal/item                           ← pure domain
       middleware.RequestID()/Telemetry()/…   internal/middleware                     ← middleware chain
       itemhttp.NewHandler(service)           internal/item/http                      ← item routes
       web.NewServer(cfg, …, WithRoutes(h))   internal/web                            ← gin engine
         └─ Server.Start(ctx) binds the listener and serves gin
```

**Worker** — `app worker`:

```
cmd/main.go                → cli.Execute()
  └─ newWorkerCmd → runWorker(ctx, cfg)   internal/cli/worker.go   ← composition root (explicit DI)
       messaging.New(driver)                  internal/messaging
       worker.New(consumer, …, handler)       internal/worker  (handler injected here; worker stays domain-free)
         └─ Worker.Run(ctx) consumes the topic until shutdown
```

Both composition roots share one bootstrap helper (`internal/cli/bootstrap.go`)
for the logger + telemetry singletons — construction dedup only; server and
worker are always separate processes, so there is nothing to race over.

Start at `cmd/main.go`, follow `cli.Execute` into the subcommand's `run*`
composition root — that one function constructs every concrete dependency and
injects interfaces. Nothing is wired anywhere else.

Architecture deep-dive: [ARCHITECTURE.md](ARCHITECTURE.md) · AI-assistant guide: [CLAUDE.md](CLAUDE.md)

## Renaming the Module

```bash
go mod edit -module github.com/you/your-service
grep -rl 'github.com/luisfelipecoelho/go-template' --include='*.go' . | xargs sed -i '' 's|github.com/luisfelipecoelho/go-template|github.com/you/your-service|g'
go mod tidy && make test
```

## License

MIT — see [LICENSE](LICENSE).

---

# go-template (Português)

Template de microsserviço Go 1.26 pronto para produção — layout padrão Go (`cmd`/`internal`/`pkg`) com organização por domínio, injeção de dependência explícita, HTTP com gin + worker assíncrono, OpenTelemetry, graceful shutdown, nativo para Docker e Kubernetes.

## Conteúdo

Primeira vez? Rode o [Início rápido](#início-rápido) e depois leia
[Como este template funciona](#como-este-template-funciona) — juntos levam uns
dez minutos e cobrem a maior parte do que você precisa.

| Seção | Leia quando quiser… |
|---|---|
| [Funcionalidades](#funcionalidades) | ver o que já vem pronto |
| [Início rápido](#início-rápido) | rodar localmente agora |
| [Docker e Compose](#docker-e-compose) | subir a stack local completa com telemetria |
| [Kubernetes & GitOps](#kubernetes--gitops-1) | fazer deploy e entender os cuidados de escala e auth |
| [Plugável e desacoplável](#plugável-e-desacoplável) | remover uma parte que não precisa |
| [O que o template deixa para você](#o-que-este-template-deixa-deliberadamente-para-você) | ver cada costura e o que se pluga nela (com exemplo de Kafka) |
| [Configuração](#configuração) | consultar uma variável de ambiente |
| [Estrutura do projeto](#estrutura-do-projeto) | descobrir qual diretório é dono de quê |
| [Como este template funciona](#como-este-template-funciona) | entender as três ideias por trás do layout |
| [Navegando o código](#navegando-o-código) | seguir uma requisição do `main()` até o handler |
| [Renomeando o módulo](#renomeando-o-módulo) | começar seu próprio serviço a partir daqui |

Para o raciocínio por trás do design — a regra de dependência, a sequência de
shutdown, os ADRs — veja **[ARCHITECTURE.md](ARCHITECTURE.md)**. Para onde
colocar código novo, veja **[CLAUDE.md](CLAUDE.md)**.

## Funcionalidades

Resumo: um esqueleto de serviço que você consegue implantar no primeiro dia e
ainda reconhecer um ano depois. Tudo abaixo está conectado, testado e
documentado — e as partes que você não precisa podem ser removidas sem sobras
(veja [Plugável e desacoplável](#plugável-e-desacoplável)).

- **Binário único, múltiplos pontos de entrada** — subcomandos `spf13/cobra`: `server` (HTTP), `worker` (consumidor assíncrono), `version`, `healthcheck`.
- **Organização por domínio + DI explícita** — o domínio é dono das suas interfaces (`Storer`, `EventPublisher`); toda a fiação vive em um único ponto de composição; sem framework de DI, sem globais.
- **Infraestrutura agnóstica** — armazenamento em memória e barramento de mensagens inclusos; troque por Postgres/SQS/SNS/Kafka implementando uma interface e mudando uma linha.
- **Observabilidade** — traces + métricas OpenTelemetry via OTLP (compatível com Datadog/Jaeger); logs JSON estruturados com `slog` e correlação automática de `trace_id`. Política de logs somente para erros: sinais não-erro viram métricas.
- **Resiliência** — retry com backoff exponencial + jitter e circuit breaker sensíveis a contexto; deadline por requisição que de fato cancela o trabalho downstream; graceful shutdown drenando o trabalho em andamento no SIGTERM.
- **Seguro por padrão** — auth bearer que *falha fechado* em prod, comparação de chave em tempo constante, decode JSON estrito, headers de segurança, limite de corpo da requisição, imagem distroless non-root e um `.golangci.yml` rodando `gosec`/`errorlint`/`bodyclose` no CI.
- **HTTP contract-first** — `api-spec.yaml` (OpenAPI 3.1) com lint via Spectral; domínio CRUD de exemplo em `/v1/items`; probes `/healthz` + `/readyz`.
- **Empacotamento pronto para produção** — Dockerfile multi-stage (distroless, non-root, binário estático), docker-compose com collector OTel e uma base Kustomize preparada para rollout sem downtime: Deployments de server + worker, Service, HPA, PodDisruptionBudget, hook `preStop` de drenagem, topology spread e ConfigMap com hash.

## Pré-requisitos

- Go 1.26+
- make
- Docker (+ Compose v2) — para a stack local
- kubectl (com kustomize) — para deploys
- `make tools` instala golangci-lint e govulncheck

## Início rápido

```bash
git clone https://github.com/luisfelipecoelho/go-template meu-servico
cd meu-servico
make run                        # servidor HTTP na porta :8080
```

```bash
curl localhost:8080/healthz
curl -X POST localhost:8080/v1/items \
  -H 'Content-Type: application/json' \
  -d '{"name":"widget","quantity":3,"price_cents":1990}'
curl localhost:8080/v1/items
```

Testes, lint e cobertura (≥85% obrigatório):

```bash
make test lint cover
```

## Docker e Compose

```bash
make docker-build               # imagem distroless, non-root, binário estático
make compose-up                 # server + worker + collector OTel
docker compose logs otel-collector   # veja traces/métricas exportados
make compose-down
```

## Kubernetes & GitOps

```bash
make k8s-render ENV=dev         # renderiza um overlay (dev|staging|prod)
make k8s-apply  ENV=prod        # aplica base + patches por ambiente
```

O `base` do Kustomize + `overlays/{dev,staging,prod}` mantêm a configuração declarativa no git. O base usa um **`configMapGenerator`** (nome com hash), então uma mudança de config commitada reescreve a referência do ConfigMap no Deployment e dispara um rolling update — o novo valor chega aos pods em execução sem operator. Segredos ficam fora do git: `overlays/prod/external-secret.yaml` referencia seu cofre via External Secrets Operator (troque por SOPS/sealed-secrets). O `ops/argocd/application.yaml` torna o repositório a fonte da verdade (Flux funciona igual). Veja **[ops/README.md](ops/README.md)** para o fluxo GitOps completo, rotação de segredos e a estratégia de repositório separado em escala. O liveness aponta para `/healthz`, o readiness para `/readyz`; no SIGTERM o pod derruba o readiness, drena as requisições em andamento, faz flush da telemetria e encerra antes do grace period de 30s.

O base entrega dois Deployments a partir da mesma imagem (ADR-1): `go-template` (`args: ["server"]`, atrás do Service, escalado pelo HPA) e `go-template-worker` (`args: ["worker"]`, sem Service e sem probes — não sobe listener). Eles são separados por um label `app.kubernetes.io/component`, então o Service nunca roteia tráfego HTTP para um pod de worker. O worker fica ocioso enquanto `MESSAGING_DRIVER=memory`, porque esse barramento é in-process — ele passa a valer quando o driver apontar para um broker real.

> **Cuidado com escala**: o HPA base usa `minReplicas: 1` de propósito, e o overlay de prod mantém assim. O `Storer` de referência (`adapters.Database`) é um mapa em memória, um por pod — mais de uma réplica contra ele divide seus dados silenciosamente entre pods (um item criado num pod dá 404 em outro, sem nada nos logs explicando). Troque o `Storer` por um banco compartilhado real antes de subir `minReplicas` acima de 1; `maxReplicas` já é 20, então o HPA ainda absorve carga acima desse piso.

> **Auth é fail-secure em prod**: `APP_ENV=prod` com `AUTH_ENABLED=false` faz o processo recusar a inicialização, então uma API sem autenticação nunca é exposta por herdar um default. O base define `AUTH_ENABLED=true`, as chaves vêm do Secret (`overlays/prod/external-secret.yaml`), e um pod com auth ligado e sem `AUTH_API_KEYS` falha rápido em vez de servir aberto. Se a autenticação realmente vive no seu gateway ou service mesh, desative explicitamente com `AUTH_ALLOW_INSECURE_NO_AUTH=true`.

## Plugável e Desacoplável

Cada limite de infraestrutura é escolhido por uma factory dirigida por config — trocar ou remover uma parte é uma env var + um pacote:

- **Mensageria** (`MESSAGING_DRIVER`) resolve via `internal/messaging.New`. Adicionar adaptador = novo pacote + um `case`; remover = apagar o pacote + o case. A interface e os call sites não mudam.
- **Cache** (`CACHE_DRIVER`) resolve via `internal/cache.New` e decora o store com `adapters.NewCache` — roda *ao lado* do banco, nunca o substitui.
- **Desacople mensageria** com `MESSAGING_DRIVER=none` (publisher/consumer no-op). **Desacople** o worker, o servidor HTTP ou o cliente de saída apagando o pacote — nada mais quebra.
- **Telemetria** é OTLP neutra: aponte `OTEL_EXPORTER_OTLP_ENDPOINT` para Datadog, Jaeger, Tempo, Grafana — sem mudar código.
- **Chamadas de saída**: `internal/web/client` — cliente resiliente (retry + circuit breaker + OTel) para BFF/ingestão.

## O Que Este Template Deixa Deliberadamente Para Você

Este é um **ponto de partida**, não um serviço pronto. Tudo abaixo é uma
interface com um default executável por trás — o template entrega a *costura*,
você pluga o que a sua aplicação realmente precisa. Nada aqui está faltando por
descuido.

| Costura (a interface) | Vem com | Você pluga | Onde |
|---|---|---|---|
| `item.Storer` | mapa em memória | Postgres, DynamoDB, … | novo arquivo em `internal/item/adapters/` + uma linha na composição |
| `messaging.Publisher` / `Consumer` | barramento em memória, `none` | Kafka, RabbitMQ, SQS/SNS, NATS | implementar 2 métodos + um `case` em `internal/messaging/factory.go` |
| `middleware.Authenticator` | chaves de API estáticas | JWT, OIDC, mTLS | novo tipo em `internal/auth/` + uma linha na chain |
| `cache.Cache` | `none` / `memory` / `redis` | já é real — Redis pronto para produção | só variáveis de ambiente |
| `middleware.Limiter` | semáforo in-process | limitador distribuído | implementar `Acquire`/`Release` |
| Ingress / Gateway | — | seu controller (nginx, ALB, Gateway API) | adicionar manifesto em `ops/k8s/` |
| Alertas & SLOs | métricas já são emitidas | seu `PrometheusRule` / dashboards | sua stack de monitoração |
| Autorização (tenancy, propriedade) | — | seu modelo — o template faz só autenticação | `internal/item` + handlers |

A regra que o template segue: **nenhum tipo de SDK de broker, de driver ou de
cloud pode aparecer numa interface.** É isso que mantém tudo isso substituível
sem tocar no domínio, no worker ou em qualquer call site.

### Exemplo: plugando Kafka

Três pontos de contato, nenhum deles no seu domínio:

```go
// 1. internal/messaging/kafka.go — satisfaça as duas interfaces.
//    Nenhum tipo do kafka nas assinaturas; Message/Handler são o contrato.
type Kafka struct{ /* seu client */ }

func (k *Kafka) Publish(ctx context.Context, msg messaging.Message) error { … }
func (k *Kafka) Consume(ctx context.Context, topic string, h messaging.Handler) error { … }
func (k *Kafka) Close() error { … }
```

```go
// 2. internal/messaging/factory.go — um case.
case "kafka":
    k := NewKafka(...)
    return k, k, k, nil
```

```bash
# 3. Defina o driver. Nenhuma outra mudança de código — o domínio publica via
#    item.EventPublisher e nunca fica sabendo que existe um broker.
MESSAGING_DRIVER=kafka
```

Remover é o inverso: apague o arquivo e o `case`. Para desacoplar a mensageria
por completo, use `MESSAGING_DRIVER=none` (o domínio recebe um publisher no-op)
ou apague o pacote conforme *Plugável e Desacoplável* acima.

## Configuração

Cada ajuste é uma variável de ambiente — não existe arquivo de configuração, e
não deveria existir. Os valores são validados na inicialização e o processo se
recusa a subir com qualquer coisa inválida ou fora de faixa, então um erro de
digitação falha na hora, em vez de no meio da primeira requisição.

Localmente, `cp .env.example .env` e edite; no Kubernetes as mesmas chaves vêm
de um ConfigMap, e os segredos de um Secret. As duas linhas que valem ler antes
de qualquer deploy são `AUTH_ENABLED` e `CACHE_REDIS_TLS`.

| Variável | Padrão | Descrição |
|----------|--------|-----------|
| `APP_ENV` | `dev` | `dev` ou `prod` (modo do gin) |
| `HTTP_PORT` | `8080` | porta HTTP |
| `HTTP_READ_TIMEOUT` | `5s` | timeout de leitura |
| `HTTP_WRITE_TIMEOUT` | `10s` | timeout de escrita |
| `HTTP_IDLE_TIMEOUT` | `60s` | timeout de keep-alive |
| `HTTP_HANDLER_TIMEOUT` | `8s` | deadline por requisição no contexto do handler (cancela o trabalho downstream → 504); mantenha abaixo de `HTTP_WRITE_TIMEOUT` |
| `SHUTDOWN_TIMEOUT` | `20s` | limite do graceful shutdown |
| `LOG_LEVEL` | `error` | somente erros em prod; use `debug` localmente |
| `MAX_BODY_BYTES` | `1048576` | limite do corpo da requisição |
| `MESSAGING_DRIVER` | `memory` | adaptador de mensageria: `memory` ou `none` (desacopla) |
| `CACHE_DRIVER` | `none` | cache read-through: `none` \| `memory` \| `redis` (roda ao lado do store) |
| `CACHE_TTL` | `5m` | tempo de vida da entrada de cache |
| `CACHE_REDIS_ADDR` | `localhost:6379` | endereço compatível com Redis (Redis/Valkey/KeyDB/Dragonfly) |
| `CACHE_REDIS_TLS` | `false` | criptografa o tráfego Redis — **use `true` para qualquer Redis gerenciado/remoto**, senão a senha e os valores em cache trafegam em texto puro |
| `CACHE_REDIS_DB` | `0` | índice do banco lógico do Redis (0..15) |
| `CACHE_REDIS_PASSWORD` | — | senha do Redis (**segredo**; via env/Secret, nunca commitada) |
| `AUTH_ENABLED` | `false` | exige bearer token em `/v1` (**desligado por padrão — ligue antes de expor o serviço**) |
| `AUTH_API_KEYS` | — | chaves bearer separadas por vírgula (**segredo**; obrigatório com auth on) |
| `THROTTLE_ENABLED` | `false` | limite de concorrência por instância (backpressure, não rate limit) |
| `THROTTLE_MAX_INFLIGHT` | `256` | máximo de requisições `/v1` concorrentes ao throttle |
| `AUTH_ALLOW_INSECURE_NO_AUTH` | `false` | desativa a trava de auth em prod — só quando um gateway/mesh garante a autenticação |
| `OTEL_ENABLED` | `false` | habilita exportação de traces + métricas |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | endpoint OTLP gRPC |
| `OTEL_SERVICE_NAME` | `go-template` | nome do serviço |
| `OTEL_SAMPLE_RATIO` | `1.0` | taxa de amostragem de traces 0..1 |

## Estrutura do Projeto

O layout segue a convenção padrão da comunidade Go: `cmd/` (entrypoint fino),
`internal/` (código de aplicação privado, organizado **por domínio** — um
diretório por domínio reúne entidade, ports, adaptadores e handler HTTP
juntos) e `pkg/` (só os pacotes sem dependências, genuinamente reutilizáveis
como biblioteca). Sem convenção de sufixo `*bus`/`*app`/`*db` — os pacotes são
desambiguados pelo caminho de import.

```
cmd/
  main.go                              delega para o cobra
internal/
  cli/                                 cmds cobra + composição (server, worker) + bootstrap compartilhado
  item/                                domínio: entidade, invariantes, Service, ports (sem gin/http)
    adapters/                          Database (Storer em memória), CacheStore (decorator), Publisher (eventos)
    http/                              adaptador HTTP do item: handler, models, mapeamento de erro
  config/                              configuração tipada via env
  auth/                                autenticador bearer (StaticKeys)
  middleware/                          middleware gin (RequestID, Telemetry, Recovery, Auth, Throttle, …)
  web/                                 server gin, health, envelope de erro, BindJSON  (+ web/client)
  messaging/ cache/ logger/ telemetry/ lifecycle/ worker/
pkg/
  uid/ resilience/                     bibliotecas genéricas sem dependências
ops/                                  docker, k8s (Kustomize), compose, otel-collector
```

`internal/item` (o domínio) não importa gin/http/broker — os adaptadores
(`internal/item/adapters`, `internal/item/http`) dependem dele, nunca o
contrário.

## Como Este Template Funciona

Três ideias tornam este código previsível de estender:

1. **Pacote por domínio, não por camada.** Tudo sobre o domínio `item` — sua
   entidade, regras de negócio, ports, adaptadores de storage/cache/mensageria
   e o handler HTTP — vive numa única árvore de diretórios,
   `internal/item/`. Nada sobre `item` vive em outro lugar. Adicionar um novo
   domínio significa copiar essa árvore, não mexer em quatro diretórios
   diferentes.
2. **O domínio é dono dos seus contratos.** `internal/item/ports.go` declara
   `Storer` e `EventPublisher`; `internal/item/adapters/` e
   `internal/item/http/` dependem do domínio, nunca o contrário. É isso que
   torna trocar storage/broker uma mudança de um arquivo (veja *Plugável e
   Desacoplável* acima) em vez de um refactor.
3. **Um único ponto de composição.** `internal/cli/server.go` e
   `.../worker.go` são os únicos lugares onde tipos concretos são construídos
   e conectados. Todo o resto recebe suas dependências como interfaces via
   construtor — sem globais, sem service locator, sem fiação escondida.

A maior parte da infraestrutura é opcional por design (mensageria, cache,
auth, o servidor HTTP inteiro, até o worker) e pode ser apagada sem tocar no
resto — veja *Plugável e Desacoplável* acima. O que é essencial é o ponto de
composição, a configuração, e o domínio que você estiver estendendo.

**Usando um assistente de IA neste repositório?** O [CLAUDE.md](CLAUDE.md)
tem uma tabela de decisão estruturada sobre onde colocar código novo (novo
domínio, nova entidade, novo endpoint, novo adaptador, ...) e o que ler antes
de fazer mudanças estruturais — aponte o assistente para lá primeiro.

## Navegando o Código

Dois apps independentes num único binário, escolhidos por subcomando cobra —
implante e escale separadamente (pods de server vs worker).

**Servidor HTTP** — `app server`:

```
cmd/main.go                → cli.Execute()                          (internal/cli/root.go, cobra)
  └─ newServerCmd → runServer(ctx, cfg)   internal/cli/server.go     ← composição (DI explícita)
       adapters.NewDatabase() (+ adapters.NewCache se CACHE_DRIVER)  → adapters.NewPublisher(messaging.New())
       item.NewService(store, publisher)  → middleware.RequestID()/…  → itemhttp.NewHandler(service)
       web.NewServer(…, WithRoutes(h))       → Server.Start(ctx) abre o listener e serve o gin
```

**Worker** — `app worker`:

```
cmd/main.go                → cli.Execute()
  └─ newWorkerCmd → runWorker(ctx, cfg)   internal/cli/worker.go   ← composição (DI explícita)
       messaging.New(driver) → worker.New(consumer, …, handler) → Worker.Run(ctx) consome o tópico até o shutdown
```

As duas composições compartilham um único bootstrap (`internal/cli/bootstrap.go`)
para os singletons de logger + telemetria — só elimina duplicação de
construção; server e worker são sempre processos separados, então não há nada
a disputar entre eles.

Comece em `cmd/main.go`, siga `cli.Execute` até o `run*` do subcomando — essa
função constrói cada dependência concreta e injeta interfaces. Nada é montado
em outro lugar.

Detalhes de arquitetura: [ARCHITECTURE.md](ARCHITECTURE.md) · Guia para assistentes de IA: [CLAUDE.md](CLAUDE.md)

## Renomeando o Módulo

```bash
go mod edit -module github.com/voce/seu-servico
grep -rl 'github.com/luisfelipecoelho/go-template' --include='*.go' . | xargs sed -i '' 's|github.com/luisfelipecoelho/go-template|github.com/voce/seu-servico|g'
go mod tidy && make test
```

## Licença

MIT — veja [LICENSE](LICENSE).
