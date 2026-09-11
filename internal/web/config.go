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
	Port        int
	ReadTimeout time.Duration
	// ReadHeaderTimeout bounds the header read independently of ReadTimeout, so
	// raising ReadTimeout to accept slow bodies cannot reopen Slowloris.
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	// MaxHeaderBytes caps request headers. net/http defaults to 1 MiB; setting
	// it explicitly makes the bound reviewable instead of inherited.
	MaxHeaderBytes int
	Env            string // "dev" enables gin debug mode; anything else is release
	Version        string
}
