// Package auth provides authenticators for the HTTP transport's Authenticator
// seam (pkg/httpx). StaticKeys — the template default —
// validates bearer tokens against a fixed allow-list with a constant-time
// comparison. Swap it for JWT/OIDC by adding a type with the same Authenticate
// method and wiring it in the composition root; the transport is unchanged.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
)

// ErrUnauthorized reports that a token matched no configured key. Transports map
// it to 401 with errors.Is — never by string.
var ErrUnauthorized = errors.New("unauthorized")

// StaticKeys authenticates bearer tokens against a fixed set of API keys. The
// zero value is unusable; construct via NewStaticKeys.
type StaticKeys struct {
	keys [][]byte
}

// NewStaticKeys builds a StaticKeys authenticator from raw key strings. Blank
// keys are ignored; it errors when no usable key remains so a misconfigured
// "auth enabled, no keys" fails fast at startup rather than allowing all.
func NewStaticKeys(keys ...string) (*StaticKeys, error) {
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out = append(out, []byte(k))
	}
	if len(out) == 0 {
		return nil, errors.New("auth: at least one API key is required")
	}
	return &StaticKeys{keys: out}, nil
}

// Authenticate returns nil when token matches a configured key and
// ErrUnauthorized otherwise. Every key is compared in constant time and the
// loop never short-circuits, so neither a match position nor key length leaks
// through timing.
func (s *StaticKeys) Authenticate(_ context.Context, token string) error {
	tok := []byte(token)
	match := 0
	for _, k := range s.keys {
		match |= subtle.ConstantTimeCompare(k, tok)
	}
	if match == 1 {
		return nil
	}
	return ErrUnauthorized
}
