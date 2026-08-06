# internal/config

The single source of truth for runtime configuration. Reads exclusively from
the process environment (12-factor) and fails fast on any unparsable or
out-of-range value — there is no config file, and there never should be one
(see the GitOps section of the root `README.md` for why: config changes ship
declaratively through `ops/k8s`, not by editing a file in the image).

## What's here

- `config.go` — `Config` (the full struct) and `Load()`. Uses an "errors are
  values" accumulator (`parser`): every field is read in one declarative
  pass via `p.str`/`p.intRange`/`p.duration`/`p.enum`/`p.boolean`/`p.csv`/...,
  each line pairing the env var name, default, and constraints; a single
  error check at the end reports the *first* invalid value.

## When to extend

Add a new config knob as **one line** in the `Config` struct literal inside
`load()`, using whichever `parser` method matches its type and constraint
(see the existing fields for the pattern — e.g. `p.intRange("HTTP_PORT",
8080, 1, 65535)`). Document the env var name and default in the field's
comment; the root `README.md`'s Configuration table should also get a row.

Never read `.env`/`.envrc` from code — secrets come from the process
environment only (K8s Secret via External Secrets Operator in production),
never from a file. Never log the full `Config` struct — only whitelisted,
non-secret fields.

## Detaching

Not applicable — every command (`server`, `worker`, `healthcheck`) calls
`config.Load()` first thing; it's load-bearing infrastructure, not an
optional boundary.
