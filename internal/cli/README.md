# internal/cli

The composition root. This is the **only** package in the module where
concrete types get constructed and wired together — every other package
receives its dependencies as interfaces through a constructor. If you're
looking for "where does X get created," it's here.

## What's here

- `root.go` — assembles the single-binary cobra command tree (`server`,
  `worker`, `version`, `healthcheck`) and `Execute()`, called by `cmd/main.go`.
- `server.go` — `runServer`: constructs the store, cache, messaging
  publisher, `item.Service`, middleware chain, and `web.Server`, then runs
  them under a `lifecycle.Runner`.
- `worker.go` — `runWorker`: constructs the messaging consumer, the
  injected message handler, and `worker.Worker`, then runs it under its own
  `lifecycle.Runner`.
- `bootstrap.go` — `newObservability`: the one shared helper both
  composition roots call for the logger + telemetry singletons. This is
  construction dedup only — `server` and `worker` are always separate OS
  processes, never sharing a running instance.
- `healthcheck.go` — a small CLI-only HTTP client that probes the local
  `/healthz`; used as the Docker `HEALTHCHECK` command (the binary is
  distroless/no-shell, so it can't `curl`).
- `version.go` — prints version metadata injected via `-ldflags` at build
  time.

## When to extend

- **New domain**: add its construction block to `runServer` (store → cache →
  publisher → service → handler → `web.WithRoutes`), following the exact
  shape already used for `item`. See the root `CLAUDE.md` → "Adding a new
  domain" for the full step-by-step.
- **New CLI command**: add `internal/cli/<name>.go` with a `newXCmd()`
  constructor, then register it in `root.go`'s `newRootCmd`.
- **New infra dependency** (another adapter, another decorator): construct it
  in `runServer`/`runWorker` next to its peers; register its shutdown as a
  `lifecycle.Component` if it holds a resource that needs closing.

## Detaching

This package is load-bearing — it's the composition root, not an optional
boundary. You can still delete *what it constructs*: remove the `worker`
command by deleting `worker.go` + its `newWorkerCmd()` registration (see
`internal/worker/README.md`); remove the HTTP server the same way with
`server.go` (see `internal/web/README.md`). `cli` itself stays.
