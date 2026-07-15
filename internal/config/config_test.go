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
	assert.Equal(t, 10*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 60*time.Second, cfg.IdleTimeout)
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
	assert.Equal(t, 10*time.Second, cfg.HTTPClient.Timeout)
	assert.Equal(t, 3, cfg.HTTPClient.MaxRetries)
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
		"HTTP_CLIENT_TIMEOUT":         "3s",
		"HTTP_CLIENT_MAX_RETRIES":     "5",
		"OTEL_ENABLED":                "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317",
		"OTEL_SERVICE_NAME":           "my-svc",
		"OTEL_SAMPLE_RATIO":           "0.25",
	}

	cfg, err := load(mapGetter(env))
	require.NoError(t, err)

	assert.Equal(t, "prod", cfg.Env)
	assert.Equal(t, 9090, cfg.HTTPPort)
	assert.Equal(t, time.Hour, cfg.ReadTimeout)
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
	assert.Equal(t, 3*time.Second, cfg.HTTPClient.Timeout)
	assert.Equal(t, 5, cfg.HTTPClient.MaxRetries)
	assert.True(t, cfg.Otel.Enabled)
	assert.Equal(t, "collector:4317", cfg.Otel.Endpoint)
	assert.Equal(t, "my-svc", cfg.Otel.ServiceName)
	assert.InDelta(t, 0.25, cfg.Otel.SampleRatio, 1e-9)
}

func TestLoad_WhitespaceTrimmed(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapGetter(map[string]string{
		"APP_ENV":   "  prod  ",
		"HTTP_PORT": " 9091 ",
		"LOG_LEVEL": "\twarn\n",
	}))
	require.NoError(t, err)
	assert.Equal(t, "prod", cfg.Env)
	assert.Equal(t, 9091, cfg.HTTPPort)
	assert.Equal(t, "warn", cfg.LogLevel)
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
		{"retries zero", map[string]string{"HTTP_CLIENT_MAX_RETRIES": "0"}, "HTTP_CLIENT_MAX_RETRIES"},
		{"retries too high", map[string]string{"HTTP_CLIENT_MAX_RETRIES": "11"}, "HTTP_CLIENT_MAX_RETRIES"},
		{"bad client timeout", map[string]string{"HTTP_CLIENT_TIMEOUT": "nope"}, "HTTP_CLIENT_TIMEOUT"},
		{"bad auth toggle", map[string]string{"AUTH_ENABLED": "yes"}, "AUTH_ENABLED"},
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
