// Package auth provides authenticators for the HTTP transport's Authenticator
// seam (internal/middleware). StaticKeys — the template default —
// validates bearer tokens against a fixed allow-list with a constant-time
// comparison. Swap it for JWT/OIDC by adding a type with the same Authenticate
// method and wiring it in the composition root; the transport is unchanged.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

// ErrUnauthorized reports that a token matched no configured key. Transports map
// it to 401 with errors.Is — never by string.
var ErrUnauthorized = errors.New("unauthorized")

// StaticKeys authenticates bearer tokens against a fixed set of API keys. The
// zero value is unusable; construct via NewStaticKeys.
//
// Keys are held as SHA-256 digests rather than raw bytes. That is not about
// storing them safely — they are already in memory — but about comparison
// width: subtle.ConstantTimeCompare returns immediately when the two lengths
// differ, so comparing raw keys is constant-time in CONTENT but not in LENGTH.
// A benchmark makes the leak obvious: a 4-byte token against 64 configured
// 44-byte keys returned ~35x faster than a token of the right length, which is
// enough signal to recover the key length by timing and shrink a brute-force
// search. Hashing first makes every comparison exactly 32 bytes wide, so
// neither length nor content is observable.
//
// SHA-256 (not a password KDF) is the right primitive here: API keys are
// high-entropy random values, not user-chosen passwords, so there is no
// dictionary to slow down — only a width to equalize.
type StaticKeys struct {
	keys [][sha256.Size]byte
}

// NewStaticKeys builds a StaticKeys authenticator from raw key strings. Blank
// keys are ignored; it errors when no usable key remains so a misconfigured
// "auth enabled, no keys" fails fast at startup rather than allowing all.
func NewStaticKeys(keys ...string) (*StaticKeys, error) {
	out := make([][sha256.Size]byte, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out = append(out, sha256.Sum256([]byte(k)))
	}
	if len(out) == 0 {
		return nil, errors.New("auth: at least one API key is required")
	}
	return &StaticKeys{keys: out}, nil
}

// Authenticate returns nil when token matches a configured key and
// ErrUnauthorized otherwise.
//
// Three properties together make it timing-safe, and all three are load-bearing:
// the token is hashed first so every comparison is a fixed 32 bytes regardless
// of the token's length; each comparison is constant-time in its content; and
// the loop never short-circuits, so the position of the matching key is not
// observable either. Rewriting this as an early `return nil` on match, or as a
// map lookup, reintroduces a timing oracle.
func (s *StaticKeys) Authenticate(_ context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	match := 0
	for _, k := range s.keys {
		match |= subtle.ConstantTimeCompare(k[:], sum[:])
	}
	if match == 1 {
		return nil
	}
	return ErrUnauthorized
}
