// Package httpx is the HTTP transport: a gin server implementing api-spec.yaml,
// with a middleware chain that enforces the errors-only logging policy (ADR-6)
// by emitting request metrics for every response and logging only on 5xx.
package web

import "time"

// Config holds the HTTP server's tunables, sourced from internal/config.
type Config struct {
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	MaxBodyBytes int64
	Env          string // "dev" enables gin debug mode; anything else is release
	Version      string
}
