// Package web is the HTTP transport: a gin server implementing api-spec.yaml,
// with a middleware chain that enforces the errors-only logging policy (ADR-6)
// by emitting request metrics for every response and logging only on 5xx.
package web

import "time"

// Config holds the HTTP server's tunables, sourced from internal/config.
//
// Body size is deliberately absent: it is enforced by middleware.BodyLimit,
// which the composition root puts in the chain. Carrying a MaxBodyBytes field
// here that nothing in this package reads would imply the server enforces it.
type Config struct {
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	Env          string // "dev" enables gin debug mode; anything else is release
	Version      string
}
