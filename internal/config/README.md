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
- `Config.validate` — the cross-field rules no single parser can see, because
  they depend on a *combination* of values. One ships today, and it is
  fail-secure: **`APP_ENV=prod` together with `AUTH_ENABLED=false` refuses to
  start**, so an unauthenticated API can never reach production by inheriting
  a default. Services whose authentication genuinely lives in a gateway or
  service mesh opt out explicitly with `AUTH_ALLOW_INSECURE_NO_AUTH=true` —
  named that way on purpose, so disabling auth in prod is a typed, reviewable
  decision in git rather than an oversight.

## Timeout keys, and how they relate

`HTTP_READ_TIMEOUT` / `HTTP_WRITE_TIMEOUT` / `HTTP_IDLE_TIMEOUT` are
transport deadlines handed to `http.Server`. `HTTP_HANDLER_TIMEOUT`
(default 8s) is different in kind: it bounds the **request context**, so a
stalled dependency is actually cancelled rather than merely disconnected
(see `internal/middleware/timeout.go`). Keep it **below**
`HTTP_WRITE_TIMEOUT` so a slow request renders a 504 error envelope instead
of having its connection cut mid-write. That ordering is documented rather
than enforced — it is a tuning choice, unlike the auth guard above, which is
a security posture and therefore does refuse to boot.

## When to extend

Add a new config knob as **one line** in the `Config` struct literal inside
`load()`, using whichever `parser` method matches its type and constraint
(see the existing fields for the pattern — e.g. `p.intRange("HTTP_PORT",
8080, 1, 65535)`). Document the env var name and default in the field's
comment; the root `README.md`'s Configuration table should also get a row.

A rule that outranks convenience: **never add a key nothing reads.** Config
that is parsed but never consumed misrepresents what the service does — add
the key in the same change that adds its call site, not in anticipation of
one. If a constraint spans two keys, it belongs in `validate`, not in a
parser method.

Never read `.env`/`.envrc` from code — secrets come from the process
environment only (K8s Secret via External Secrets Operator in production),
never from a file. Never log the full `Config` struct — only whitelisted,
non-secret fields.

## Detaching

Not applicable — every command (`server`, `worker`, `healthcheck`) calls
`config.Load()` first thing; it's load-bearing infrastructure, not an
optional boundary.
