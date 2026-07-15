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
	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT (default 20s)
	LogLevel        string        // LOG_LEVEL: debug|info|warn|error (default error — errors-only logging policy; non-error signals are metrics)
	MaxBodyBytes    int64         // MAX_BODY_BYTES (default 1048576, >0)
	Cache           CacheConfig
	Messaging       MessagingConfig
	Auth            AuthConfig
	Throttle        ThrottleConfig
	HTTPClient      HTTPClientConfig
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
}

// AuthConfig toggles bearer-token authentication on the /v1 group. When Enabled
// is true the composition root builds a static-key authenticator from APIKeys
// (which are secrets: sourced from the environment, never logged, never
// committed). Swap the concrete authenticator for JWT/OIDC in internal/auth.
type AuthConfig struct {
	Enabled bool     // AUTH_ENABLED (default false)
	APIKeys []string // AUTH_API_KEYS: comma-separated bearer keys (secret)
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

// HTTPClientConfig tunes the resilient outbound client in pkg/httpclient.
type HTTPClientConfig struct {
	Timeout    time.Duration // HTTP_CLIENT_TIMEOUT (default 10s)
	MaxRetries int           // HTTP_CLIENT_MAX_RETRIES (default 3, >=1 total attempts)
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
		ShutdownTimeout: p.duration("SHUTDOWN_TIMEOUT", 20*time.Second),
		LogLevel:        p.enum("LOG_LEVEL", "error", "debug", "info", "warn", "error"),
		MaxBodyBytes:    p.int64Min("MAX_BODY_BYTES", 1048576, 1),
		// Driver names are validated by their factory (pkg/messaging.New), so
		// adding an adapter is a single-package change.
		Cache: CacheConfig{
			Driver:        p.str("CACHE_DRIVER", "none"),
			TTL:           p.duration("CACHE_TTL", 5*time.Minute),
			RedisAddr:     p.str("CACHE_REDIS_ADDR", "localhost:6379"),
			RedisPassword: p.str("CACHE_REDIS_PASSWORD", ""),
			RedisDB:       p.intRange("CACHE_REDIS_DB", 0, 0, 15),
		},
		Messaging: MessagingConfig{Driver: p.str("MESSAGING_DRIVER", "memory")},
		Auth: AuthConfig{
			Enabled: p.boolean("AUTH_ENABLED", false),
			APIKeys: p.csv("AUTH_API_KEYS"),
		},
		Throttle: ThrottleConfig{
			Enabled:     p.boolean("THROTTLE_ENABLED", false),
			MaxInFlight: p.intRange("THROTTLE_MAX_INFLIGHT", 256, 1, 1_000_000),
		},
		HTTPClient: HTTPClientConfig{
			Timeout:    p.duration("HTTP_CLIENT_TIMEOUT", 10*time.Second),
			MaxRetries: p.intRange("HTTP_CLIENT_MAX_RETRIES", 3, 1, 10),
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
	return cfg, nil
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

func (p *parser) str(key, def string) string {
	return strVal(p.get, key, def)
}

// csv reads a comma-separated list, trimming blanks. It never fails (an empty
// or unset value yields nil), so it needs no error accumulation.
func (p *parser) csv(key string) []string {
	return csvVal(p.get, key)
}

func (p *parser) enum(key, def string, allowed ...string) string {
	if p.err != nil {
		return def
	}
	v, err := enumVal(p.get, key, def, allowed...)
	p.fail(err)
	return v
}

func (p *parser) intRange(key string, def, min, max int) int {
	if p.err != nil {
		return def
	}
	v, err := intVal(p.get, key, def, min, max)
	p.fail(err)
	return v
}

func (p *parser) int64Min(key string, def, min int64) int64 {
	if p.err != nil {
		return def
	}
	v, err := int64Val(p.get, key, def, min)
	p.fail(err)
	return v
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	if p.err != nil {
		return def
	}
	v, err := durVal(p.get, key, def)
	p.fail(err)
	return v
}

func (p *parser) boolean(key string, def bool) bool {
	if p.err != nil {
		return def
	}
	v, err := boolVal(p.get, key, def)
	p.fail(err)
	return v
}

func (p *parser) ratio(key string, def float64) float64 {
	if p.err != nil {
		return def
	}
	v, err := ratioVal(p.get, key, def)
	p.fail(err)
	return v
}

// strVal returns the trimmed value of key, or def when unset/blank.
func strVal(get func(string) string, key, def string) string {
	if v := strings.TrimSpace(get(key)); v != "" {
		return v
	}
	return def
}

// csvVal reads a comma-separated list, trimming each element and dropping
// blanks. Unset or all-blank yields nil.
func csvVal(get func(string) string, key string) []string {
	raw := strings.TrimSpace(get(key))
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

// enumVal reads a trimmed string restricted to the allowed set.
func enumVal(get func(string) string, key, def string, allowed ...string) (string, error) {
	v := strVal(get, key, def)
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fmt.Errorf("config %s: %q is not one of %s", key, v, strings.Join(allowed, "|"))
}

// intVal reads an int within [min, max] inclusive.
func intVal(get func(string) string, key string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config %s: not a valid integer: %w", key, err)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("config %s: %d out of range [%d, %d]", key, n, min, max)
	}
	return n, nil
}

// int64Val reads an int64 that must be >= min.
func int64Val(get func(string) string, key string, def, min int64) (int64, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("config %s: not a valid integer: %w", key, err)
	}
	if n < min {
		return 0, fmt.Errorf("config %s: %d must be >= %d", key, n, min)
	}
	return n, nil
}

// durVal reads a non-negative duration via time.ParseDuration.
func durVal(get func(string) string, key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config %s: not a valid duration: %w", key, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("config %s: %s must not be negative", key, d)
	}
	return d, nil
}

// boolVal reads a bool via strconv.ParseBool.
func boolVal(get func(string) string, key string, def bool) (bool, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config %s: not a valid boolean: %w", key, err)
	}
	return b, nil
}

// ratioVal reads a float in [0, 1] inclusive.
func ratioVal(get func(string) string, key string, def float64) (float64, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("config %s: not a valid number: %w", key, err)
	}
	if f < 0 || f > 1 {
		return 0, fmt.Errorf("config %s: %v out of range [0, 1]", key, f)
	}
	return f, nil
}
