// config_test.go
package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mapGetter builds a getter seam backed by a fixed map so tests never touch the
// real process environment.
func mapGetter(env map[string]string) func(string) string {
	return func(key string) string {
		return env[key]
	}
}

func TestLoad_EmptyEnvYieldsDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapGetter(map[string]string{}))
	require.NoError(t, err)

	assert.Equal(t, "dev", cfg.Env)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, 5*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 2*time.Second, cfg.ReadHeaderTimeout)
	assert.Equal(t, 10*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 60*time.Second, cfg.IdleTimeout)
	assert.Equal(t, 1048576, cfg.MaxHeaderBytes)
	assert.False(t, cfg.Pprof.Enabled, "profiling must be off unless asked for")
	assert.Equal(t, "127.0.0.1:6060", cfg.Pprof.Addr, "profiling binds loopback by default")
	assert.Equal(t, 20*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "error", cfg.LogLevel)
	assert.Equal(t, int64(1048576), cfg.MaxBodyBytes)
	assert.Equal(t, "none", cfg.Cache.Driver)
	assert.Equal(t, 5*time.Minute, cfg.Cache.TTL)
	assert.Equal(t, "localhost:6379", cfg.Cache.RedisAddr)
	assert.Equal(t, 0, cfg.Cache.RedisDB)
	assert.Equal(t, "memory", cfg.Messaging.Driver)
	assert.False(t, cfg.Auth.Enabled)
	assert.Empty(t, cfg.Auth.APIKeys)
	assert.False(t, cfg.Throttle.Enabled)
	assert.Equal(t, 256, cfg.Throttle.MaxInFlight)
	assert.False(t, cfg.Otel.Enabled)
	assert.Equal(t, "localhost:4317", cfg.Otel.Endpoint)
	assert.Equal(t, "go-template", cfg.Otel.ServiceName)
	assert.InDelta(t, 1.0, cfg.Otel.SampleRatio, 1e-9)
}

func TestLoad_ValidOverrides(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"APP_ENV":                     "prod",
		"HTTP_PORT":                   "9090",
		"HTTP_READ_TIMEOUT":           "1h",
		"HTTP_READ_HEADER_TIMEOUT":    "3s",
		"HTTP_MAX_HEADER_BYTES":       "4096",
		"HTTP_WRITE_TIMEOUT":          "2s",
		"HTTP_IDLE_TIMEOUT":           "30s",
		"SHUTDOWN_TIMEOUT":            "15s",
		"LOG_LEVEL":                   "debug",
		"MAX_BODY_BYTES":              "2048",
		"CACHE_DRIVER":                "memory",
		"CACHE_TTL":                   "90s",
		"CACHE_REDIS_ADDR":            "redis:6379",
		"CACHE_REDIS_DB":              "3",
		"MESSAGING_DRIVER":            "none",
		"AUTH_ENABLED":                "true",
		"AUTH_API_KEYS":               " k1 , k2 ,, ",
		"THROTTLE_ENABLED":            "true",
		"THROTTLE_MAX_INFLIGHT":       "64",
		"OTEL_ENABLED":                "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317",
		"OTEL_SERVICE_NAME":           "my-svc",
		"OTEL_SAMPLE_RATIO":           "0.25",
		"PPROF_ENABLED":               "true",
		"PPROF_ADDR":                  "127.0.0.1:7070",
	}

	cfg, err := load(mapGetter(env))
	require.NoError(t, err)

	assert.Equal(t, "prod", cfg.Env)
	assert.Equal(t, 9090, cfg.HTTPPort)
	assert.Equal(t, time.Hour, cfg.ReadTimeout)
	assert.Equal(t, 3*time.Second, cfg.ReadHeaderTimeout)
	assert.Equal(t, 4096, cfg.MaxHeaderBytes)
	assert.Equal(t, 2*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 30*time.Second, cfg.IdleTimeout)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, int64(2048), cfg.MaxBodyBytes)
	assert.Equal(t, "memory", cfg.Cache.Driver)
	assert.Equal(t, 90*time.Second, cfg.Cache.TTL)
	assert.Equal(t, "redis:6379", cfg.Cache.RedisAddr)
	assert.Equal(t, 3, cfg.Cache.RedisDB)
	assert.Equal(t, "none", cfg.Messaging.Driver)
	assert.True(t, cfg.Auth.Enabled)
	assert.Equal(t, []string{"k1", "k2"}, cfg.Auth.APIKeys, "csv trimmed, blanks dropped")
	assert.True(t, cfg.Throttle.Enabled)
	assert.Equal(t, 64, cfg.Throttle.MaxInFlight)
	assert.True(t, cfg.Otel.Enabled)
	assert.Equal(t, "collector:4317", cfg.Otel.Endpoint)
	assert.Equal(t, "my-svc", cfg.Otel.ServiceName)
	assert.InDelta(t, 0.25, cfg.Otel.SampleRatio, 1e-9)
	assert.True(t, cfg.Pprof.Enabled)
	assert.Equal(t, "127.0.0.1:7070", cfg.Pprof.Addr)
}

func TestLoad_WhitespaceTrimmed(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapGetter(map[string]string{
		"APP_ENV":      "  prod  ",
		"HTTP_PORT":    " 9091 ",
		"LOG_LEVEL":    "\twarn\n",
		"AUTH_ENABLED": "true", // prod requires an explicit auth posture
	}))
	require.NoError(t, err)
	assert.Equal(t, "prod", cfg.Env)
	assert.Equal(t, 9091, cfg.HTTPPort)
	assert.Equal(t, "warn", cfg.LogLevel)
}

// The prod auth guard is a fail-secure gate, so all three of its outcomes are
// pinned: prod without auth refuses to start, prod with auth starts, and prod
// with the explicit insecure opt-out starts (for gateway/mesh-enforced authn).
func TestLoad_ProdAuthGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "prod without auth refuses to start",
			env:     map[string]string{"APP_ENV": "prod"},
			wantErr: true,
		},
		{
			name:    "prod with auth enabled starts",
			env:     map[string]string{"APP_ENV": "prod", "AUTH_ENABLED": "true"},
			wantErr: false,
		},
		{
			name:    "prod with explicit insecure opt-out starts",
			env:     map[string]string{"APP_ENV": "prod", "AUTH_ALLOW_INSECURE_NO_AUTH": "true"},
			wantErr: false,
		},
		{
			name:    "dev without auth starts",
			env:     map[string]string{"APP_ENV": "dev"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(mapGetter(tt.env))
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "AUTH_ENABLED", "error must name the fix")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestLoad_RatioBoundsInclusive(t *testing.T) {
	t.Parallel()

	for _, ratio := range []string{"0", "1", "0.5"} {
		cfg, err := load(mapGetter(map[string]string{"OTEL_SAMPLE_RATIO": ratio}))
		require.NoError(t, err, "ratio %s should be valid", ratio)
		assert.GreaterOrEqual(t, cfg.Otel.SampleRatio, 0.0)
		assert.LessOrEqual(t, cfg.Otel.SampleRatio, 1.0)
	}
}

func TestLoad_InvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantVar string // substring of variable name that must appear in the error
	}{
		{"bad int port", map[string]string{"HTTP_PORT": "abc"}, "HTTP_PORT"},
		{"port zero", map[string]string{"HTTP_PORT": "0"}, "HTTP_PORT"},
		{"port too high", map[string]string{"HTTP_PORT": "70000"}, "HTTP_PORT"},
		{"negative duration", map[string]string{"HTTP_READ_TIMEOUT": "-5s"}, "HTTP_READ_TIMEOUT"},
		{"bad duration", map[string]string{"SHUTDOWN_TIMEOUT": "notaduration"}, "SHUTDOWN_TIMEOUT"},
		{"ratio too high", map[string]string{"OTEL_SAMPLE_RATIO": "2.0"}, "OTEL_SAMPLE_RATIO"},
		{"ratio negative", map[string]string{"OTEL_SAMPLE_RATIO": "-0.1"}, "OTEL_SAMPLE_RATIO"},
		{"bad ratio", map[string]string{"OTEL_SAMPLE_RATIO": "abc"}, "OTEL_SAMPLE_RATIO"},
		{"unknown log level", map[string]string{"LOG_LEVEL": "trace"}, "LOG_LEVEL"},
		{"unknown env", map[string]string{"APP_ENV": "staging"}, "APP_ENV"},
		{"max body zero", map[string]string{"MAX_BODY_BYTES": "0"}, "MAX_BODY_BYTES"},
		{"max body negative", map[string]string{"MAX_BODY_BYTES": "-1"}, "MAX_BODY_BYTES"},
		{"bad bool", map[string]string{"OTEL_ENABLED": "yes"}, "OTEL_ENABLED"},
		{"bad auth toggle", map[string]string{"AUTH_ENABLED": "yes"}, "AUTH_ENABLED"},
		{"prod without auth is fail-secure", map[string]string{"APP_ENV": "prod"}, "AUTH_ENABLED"},
		{"throttle zero", map[string]string{"THROTTLE_MAX_INFLIGHT": "0"}, "THROTTLE_MAX_INFLIGHT"},
		{"bad cache ttl", map[string]string{"CACHE_TTL": "nope"}, "CACHE_TTL"},
		{"redis db out of range", map[string]string{"CACHE_REDIS_DB": "16"}, "CACHE_REDIS_DB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(mapGetter(tt.env))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantVar, "error must name the offending variable")
		})
	}
}

// Header limits are the Slowloris/header-bomb bounds. A bad value must stop the
// process rather than silently fall back to a default an operator thinks they
// overrode — a server running with different limits than the ones in git is the
// failure this pins.
func TestLoad_HeaderLimitsRejectBadValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"unparsable read-header timeout", map[string]string{"HTTP_READ_HEADER_TIMEOUT": "soon"}, "HTTP_READ_HEADER_TIMEOUT"},
		{"negative read-header timeout", map[string]string{"HTTP_READ_HEADER_TIMEOUT": "-1s"}, "HTTP_READ_HEADER_TIMEOUT"},
		{"zero max header bytes", map[string]string{"HTTP_MAX_HEADER_BYTES": "0"}, "HTTP_MAX_HEADER_BYTES"},
		{"negative max header bytes", map[string]string{"HTTP_MAX_HEADER_BYTES": "-1"}, "HTTP_MAX_HEADER_BYTES"},
		{"unparsable max header bytes", map[string]string{"HTTP_MAX_HEADER_BYTES": "1MB"}, "HTTP_MAX_HEADER_BYTES"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := load(mapGetter(tc.env))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want, "the error must name the offending key")
		})
	}
}

// The pprof guard is fail-secure like the auth guard: profiling exposes heap
// contents and goroutine stacks, so a production bind that is reachable off-host
// must refuse to start rather than serve. Every outcome is pinned so a later
// refactor cannot quietly widen it.
func TestLoad_PprofGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"prod, pprof off, any addr", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "false", "PPROF_ADDR": "0.0.0.0:6060",
		}, false},
		{"prod, pprof on loopback ip", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": "127.0.0.1:6060",
		}, false},
		{"prod, pprof on loopback name", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": "localhost:6060",
		}, false},
		{"prod, pprof on ipv6 loopback", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": "[::1]:6060",
		}, false},
		{"prod, pprof on all interfaces", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": "0.0.0.0:6060",
		}, true},
		{"prod, pprof on wildcard host", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": ":6060",
		}, true},
		{"prod, pprof on routable ip", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": "10.0.0.5:6060",
		}, true},
		{"prod, pprof on unresolvable name", map[string]string{
			"APP_ENV": "prod", "AUTH_ENABLED": "true",
			"PPROF_ENABLED": "true", "PPROF_ADDR": "admin.internal:6060",
		}, true},
		{"dev, pprof on all interfaces", map[string]string{
			"PPROF_ENABLED": "true", "PPROF_ADDR": "0.0.0.0:6060",
		}, false},
		{"malformed addr rejected in any env", map[string]string{
			"PPROF_ENABLED": "true", "PPROF_ADDR": "6060",
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := load(mapGetter(tc.env))
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "PPROF", "the error must name the offending key")
				return
			}
			require.NoError(t, err)
		})
	}
}
