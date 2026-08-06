# internal/auth

Concrete `Authenticator` implementations for the bearer-auth seam that
`internal/middleware` owns (`middleware.Authenticator`, in
`internal/middleware/auth.go`). This package satisfies that interface; it
never imports gin or anything HTTP-shaped.

## What's here

- `auth.go` — `StaticKeys`, the template default: validates a bearer token
  against a fixed allow-list using `crypto/subtle.ConstantTimeCompare` in a
  loop that never short-circuits, so neither a match position nor key length
  leaks through timing. `NewStaticKeys` fails construction if no usable key
  is configured, so "auth enabled, no keys" fails fast at startup instead of
  silently allowing everyone through.

## When to extend

Swap `StaticKeys` for JWT/OIDC (or any other scheme) by adding a new type
in this package with the same `Authenticate(ctx, token) error` method, then
changing the one constructor call in `internal/cli/server.go` (currently
`auth.NewStaticKeys(cfg.Auth.APIKeys...)`). `internal/middleware` and
`internal/web` need no changes — they depend on the `Authenticator`
interface, not this concrete type.

## Detaching

Auth is opt-in at the composition root: leave `AUTH_ENABLED=false` (the
default) and `middleware.Auth(...)` is never added to the chain, so this
package is never constructed. To remove it from the module entirely, delete
this package, the `AuthConfig` fields in `internal/config`, and the
`if cfg.Auth.Enabled` block in `internal/cli/server.go`.
