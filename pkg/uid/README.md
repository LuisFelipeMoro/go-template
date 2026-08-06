# pkg/uid

Generates and validates RFC 4122 version-4 UUIDs using only the standard
library (`crypto/rand`) — no UUID dependency. "A little copying beats a
little dependency."

## What's here

- `uid.go` — `New()` (a random v4 UUID in canonical 8-4-4-4-12 form,
  erroring only if the system CSPRNG is unavailable) and `Validate(s)`
  (structural check: 36 chars, hyphens at the right positions, hex
  elsewhere — deliberately lenient about version, matching what callers
  need when accepting external ids).

## When to use

- Entity IDs (`internal/item.Service.Create` calls `uid.New()`).
- Request correlation IDs (`internal/middleware.RequestID` calls
  `uid.New()` when no inbound `X-Request-ID` is present).
- Validating an externally supplied ID before using it as a lookup key
  (`internal/item.Service`'s `validateID` calls `uid.Validate`).

## Why this is under `pkg/`, not `internal/`

Zero dependency on anything under `internal/` — that's the bar `pkg/` holds
every package to (see the root `CLAUDE.md`'s decision table: "a genuinely
generic utility with ZERO dependency on anything under `internal/`"). That's
what makes it safe to import from outside this module if you ever want to.

## Detaching

Standalone; nothing depends on `internal/`, so nothing breaks elsewhere if
you delete it — just replace its two call sites with another UUID source.
