// config.go
// Package config is the single source of truth for runtime configuration.
// It reads values exclusively from the process environment and fails fast on
// any unparsable or out-of-range value.
//
// Security: configuration is sourced from the environment only — never from
// files on disk — and no default carries a credential. If template users add
// secret-bearing keys later, they MUST NOT echo the value on error (name the
// variable and the reason only) and MUST NOT log the full config at startup.
// None of the keys defined here are secrets, so naming them in errors is safe.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// getenv is the production getter seam wrapping os.Getenv.
func getenv(key string) string {
	return os.Getenv(key)
}

// Config holds every runtime knob for the application. The zero value is not
// intended for use; construct it via Load.
type Config struct {
	Env             string        // APP_ENV: dev|prod (default dev)
	HTTPPort        int           // HTTP_PORT (default 8080, 1..65535)
	ReadTimeout     time.Duration // HTTP_READ_TIMEOUT (default 5s)
	WriteTimeout    time.Duration // HTTP_WRITE_TIMEOUT (default 10s)
	IdleTimeout     time.Duration // HTTP_IDLE_TIMEOUT (default 60s)
	HandlerTimeout  time.Duration // HTTP_HANDLER_TIMEOUT (default 8s; keep below HTTP_WRITE_TIMEOUT)
	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT (default 20s)
	LogLevel        string        // LOG_LEVEL: debug|info|warn|error (default error — errors-only logging policy; non-error signals are metrics)
	MaxBodyBytes    int64         // MAX_BODY_BYTES (default 1048576, >0)
	Cache           CacheConfig
	Messaging       MessagingConfig
	Auth            AuthConfig
	Throttle        ThrottleConfig
	Otel            OtelConfig
}

// CacheConfig selects a read-through cache that fronts the storage adapter — it
// sits alongside the database, never replacing it, so a real DB and a Redis
// cache run together. Driver "none" (default) disables caching with zero
// overhead. RedisPassword is a secret: from the environment only, never logged.
type CacheConfig struct {
	Driver        string        // CACHE_DRIVER: none (default) | memory | redis
	TTL           time.Duration // CACHE_TTL (default 5m) per-entry lifetime
	RedisAddr     string        // CACHE_REDIS_ADDR (default localhost:6379)
	RedisPassword string        // CACHE_REDIS_PASSWORD (secret)
	RedisDB       int           // CACHE_REDIS_DB (default 0, 0..15)
	RedisTLS      bool          // CACHE_REDIS_TLS (default false; REQUIRED for any managed/remote Redis)
}

// AuthConfig toggles bearer-token authentication on the /v1 group. When Enabled
// is true the composition root builds a static-key authenticator from APIKeys
// (which are secrets: sourced from the environment, never logged, never
// committed). Swap the concrete authenticator for JWT/OIDC in internal/auth.
type AuthConfig struct {
	Enabled bool     // AUTH_ENABLED (default false)
	APIKeys []string // AUTH_API_KEYS: comma-separated bearer keys (secret)
	// AllowInsecureNoAuth (AUTH_ALLOW_INSECURE_NO_AUTH) is the deliberate
	// opt-out from the prod auth guard in Load. It exists for services whose
	// authentication genuinely lives in front of them (an API gateway, a
	// service mesh with mTLS). Naming it "INSECURE" is the point: disabling
	// authentication in production must be a typed, reviewable decision in
	// git, never a default someone inherited without noticing.
	AllowInsecureNoAuth bool
}

// ThrottleConfig toggles the per-instance concurrency throttle on the /v1
// group. This is backpressure (in-flight cap), not a rate limit: request-rate
// quotas belong at the border (gateway/ingress), enforced globally across
// replicas — never in the app.
type ThrottleConfig struct {
	Enabled     bool // THROTTLE_ENABLED (default false)
	MaxInFlight int  // THROTTLE_MAX_INFLIGHT (default 256, >=1) concurrent requests
}

// MessagingConfig selects the messaging adapter. "none" detaches messaging
// entirely (the domain receives a no-op publisher and no consumer runs).
type MessagingConfig struct {
	Driver string // MESSAGING_DRIVER: memory (default) | none. Extend the factory to add kafka, sqs, nats, ...
}

// OtelConfig groups OpenTelemetry-related settings.
type OtelConfig struct {
	Enabled     bool    // OTEL_ENABLED (default false)
	Endpoint    string  // OTEL_EXPORTER_OTLP_ENDPOINT (default "localhost:4317")
	ServiceName string  // OTEL_SERVICE_NAME (default "go-template")
	SampleRatio float64 // OTEL_SAMPLE_RATIO (default 1.0, 0..1)
}

// Load reads configuration from the process environment via os.Getenv.
//
// Security: values are read from the environment only; nothing here logs a
// value. Do not log the returned Config wholesale — emit only whitelisted,
// non-secret fields.
func Load() (Config, error) {
	return load(getenv)
}

// load is the testable seam behind Load; get resolves an environment variable
// name to its raw string value ("" when unset).
//
// Errors are values (Rob Pike): rather than an `if err != nil` after every
// field, a parser accumulates the first failure and every field is read in one
// declarative pass. The key, default, and constraints for each field sit
// together on one line; the single error check is at the end.
func load(get func(string) string) (Config, error) {
	p := parser{get: get}

	cfg := Config{
		Env:             p.enum("APP_ENV", "dev", "dev", "prod"),
		HTTPPort:        p.intRange("HTTP_PORT", 8080, 1, 65535),
		ReadTimeout:     p.duration("HTTP_READ_TIMEOUT", 5*time.Second),
		WriteTimeout:    p.duration("HTTP_WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:     p.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		HandlerTimeout:  p.duration("HTTP_HANDLER_TIMEOUT", 8*time.Second),
		ShutdownTimeout: p.duration("SHUTDOWN_TIMEOUT", 20*time.Second),
		LogLevel:        p.enum("LOG_LEVEL", "error", "debug", "info", "warn", "error"),
		MaxBodyBytes:    p.int64Min("MAX_BODY_BYTES", 1048576, 1),
		// Driver names are validated by their factory (internal/messaging.New), so
		// adding an adapter is a single-package change.
		Cache: CacheConfig{
			Driver:        p.str("CACHE_DRIVER", "none"),
			TTL:           p.duration("CACHE_TTL", 5*time.Minute),
			RedisAddr:     p.str("CACHE_REDIS_ADDR", "localhost:6379"),
			RedisPassword: p.str("CACHE_REDIS_PASSWORD", ""),
			RedisDB:       p.intRange("CACHE_REDIS_DB", 0, 0, 15),
			RedisTLS:      p.boolean("CACHE_REDIS_TLS", false),
		},
		Messaging: MessagingConfig{Driver: p.str("MESSAGING_DRIVER", "memory")},
		Auth: AuthConfig{
			Enabled:             p.boolean("AUTH_ENABLED", false),
			APIKeys:             p.csv("AUTH_API_KEYS"),
			AllowInsecureNoAuth: p.boolean("AUTH_ALLOW_INSECURE_NO_AUTH", false),
		},
		Throttle: ThrottleConfig{
			Enabled:     p.boolean("THROTTLE_ENABLED", false),
			MaxInFlight: p.intRange("THROTTLE_MAX_INFLIGHT", 256, 1, 1_000_000),
		},
		Otel: OtelConfig{
			Enabled:     p.boolean("OTEL_ENABLED", false),
			Endpoint:    p.str("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
			ServiceName: p.str("OTEL_SERVICE_NAME", "go-template"),
			SampleRatio: p.ratio("OTEL_SAMPLE_RATIO", 1.0),
		},
	}

	if p.err != nil {
		return Config{}, p.err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate enforces the cross-field rules that no single parser can see,
// because they depend on the combination of two values rather than either one.
func (c Config) validate() error {
	// Fail secure: authentication defaults to off so the template runs locally
	// with no setup, but that default silently becoming a production posture is
	// the classic fail-open deployment. Refuse to start rather than serve an
	// unauthenticated API, unless the operator has explicitly declared that
	// something in front handles authn.
	if c.Env == "prod" && !c.Auth.Enabled && !c.Auth.AllowInsecureNoAuth {
		return errors.New(
			"config: APP_ENV=prod with AUTH_ENABLED=false would serve an unauthenticated API; " +
				"set AUTH_ENABLED=true and provide AUTH_API_KEYS, or set " +
				"AUTH_ALLOW_INSECURE_NO_AUTH=true if authentication is enforced by a gateway or service mesh")
	}
	return nil
}

// parser reads and validates environment values, holding the first error so the
// caller checks once. Once err is set every subsequent read is skipped and
// returns its default — the accumulated error is what Load reports.
type parser struct {
	get func(string) string
	err error
}

// fail records the first error only.
func (p *parser) fail(err error) {
	if err != nil && p.err == nil {
		p.err = err
	}
}

// str returns the trimmed value of key, or def when unset/blank.
func (p *parser) str(key, def string) string {
	if v := strings.TrimSpace(p.get(key)); v != "" {
		return v
	}
	return def
}

// csv reads a comma-separated list, trimming each element and dropping
// blanks. Unset or all-blank yields nil. It never fails, so it needs no error
// accumulation.
func (p *parser) csv(key string) []string {
	raw := strings.TrimSpace(p.get(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// enum reads a trimmed string restricted to the allowed set.
func (p *parser) enum(key, def string, allowed ...string) string {
	if p.err != nil {
		return def
	}
	v := p.str(key, def)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	p.fail(fmt.Errorf("config %s: %q is not one of %s", key, v, strings.Join(allowed, "|")))
	return def
}

// intRange reads an int within [lo, hi] inclusive.
func (p *parser) intRange(key string, def, lo, hi int) int {
	if p.err != nil {
		return def
	}
	raw := strings.TrimSpace(p.get(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		p.fail(fmt.Errorf("config %s: not a valid integer: %w", key, err))
		return def
	}
	if n < lo || n > hi {
		p.fail(fmt.Errorf("config %s: %d out of range [%d, %d]", key, n, lo, hi))
		return def
	}
	return n
}

// int64Min reads an int64 that must be >= lo.
func (p *parser) int64Min(key string, def, lo int64) int64 {
	if p.err != nil {
		return def
	}
	raw := strings.TrimSpace(p.get(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		p.fail(fmt.Errorf("config %s: not a valid integer: %w", key, err))
		return def
	}
	if n < lo {
		p.fail(fmt.Errorf("config %s: %d must be >= %d", key, n, lo))
		return def
	}
	return n
}

// duration reads a non-negative duration via time.ParseDuration.
func (p *parser) duration(key string, def time.Duration) time.Duration {
	if p.err != nil {
		return def
	}
	raw := strings.TrimSpace(p.get(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		p.fail(fmt.Errorf("config %s: not a valid duration: %w", key, err))
		return def
	}
	if d < 0 {
		p.fail(fmt.Errorf("config %s: %s must not be negative", key, d))
		return def
	}
	return d
}

// boolean reads a bool via strconv.ParseBool.
func (p *parser) boolean(key string, def bool) bool {
	if p.err != nil {
		return def
	}
	raw := strings.TrimSpace(p.get(key))
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		p.fail(fmt.Errorf("config %s: not a valid boolean: %w", key, err))
		return def
	}
	return b
}

// ratio reads a float in [0, 1] inclusive.
func (p *parser) ratio(key string, def float64) float64 {
	if p.err != nil {
		return def
	}
	raw := strings.TrimSpace(p.get(key))
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		p.fail(fmt.Errorf("config %s: not a valid number: %w", key, err))
		return def
	}
	if f < 0 || f > 1 {
		p.fail(fmt.Errorf("config %s: %v out of range [0, 1]", key, f))
		return def
	}
	return f
}
