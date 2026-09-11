// wiring_test.go — covers the composition-root builders that assemble the
// server: the storage stack and the /v1 middleware chain.
package cli

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/config"
	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/item/adapters"
	"github.com/luisfelipecoelho/go-template/internal/messaging"
	"github.com/luisfelipecoelho/go-template/internal/telemetry"
	"github.com/luisfelipecoelho/go-template/internal/web"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildStorer owns the "cache runs ALONGSIDE the store" rule: a cache driver
// must decorate the store, never replace it, and "none" must cost nothing.
// These cases pin that the decorator is applied exactly when configured and
// that the returned Closer is always safe to call.
func TestBuildStorer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		driver    string
		wantCache bool
		wantErr   string
	}{
		{"unset driver skips the decorator", "", false, ""},
		{"none skips the decorator", "none", false, ""},
		{"memory wraps the store", "memory", true, ""},
		{"unknown driver fails fast", "wat", false, "building cache"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			cfg.Cache.Driver = tc.driver

			storer, closer, err := buildStorer(context.Background(), cfg, testLogger())

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, storer)
			require.NotNil(t, closer, "the Closer must never be nil: the lifecycle component calls it unconditionally")
			assert.NoError(t, closer.Close())

			if tc.wantCache {
				assert.IsType(t, &adapters.CacheStore{}, storer,
					"a configured cache must DECORATE the store, not replace it")
			} else {
				assert.IsType(t, &adapters.Database{}, storer,
					"cache off must hand back the bare store with no decorator overhead")
			}

			// Whatever came back must still honour the port end to end.
			ctx := context.Background()
			itm := item.Item{ID: "11111111-1111-4111-8111-111111111111", Name: "probe", Quantity: 1, PriceCents: 100}
			require.NoError(t, storer.Create(ctx, itm))
			got, err := storer.QueryByID(ctx, itm.ID)
			require.NoError(t, err)
			assert.Equal(t, itm.Name, got.Name)
		})
	}
}

// The chain's ORDER is load-bearing and documented on buildMiddleware, but a
// comment cannot fail. These cases assert it through behaviour instead: a
// request rejected by Auth must still carry the request id and security headers
// set above it, which is only true if RequestID and SecurityHeaders run first.
func TestBuildMiddleware_RejectionsStillCarryUpstreamHeaders(t *testing.T) {
	t.Parallel()

	cfg := validConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.APIKeys = []string{"secret-key"}

	rec := sendThroughChain(t, cfg, "", `{}`)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"),
		"RequestID must run before Auth, or a 401 is uncorrelatable in support")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"),
		"SecurityHeaders must run before Auth, or rejections ship unhardened")
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
}

func TestBuildMiddleware_AuthOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  int
	}{
		{"no token is rejected", "", http.StatusUnauthorized},
		{"wrong token is rejected", "nope", http.StatusUnauthorized},
		{"configured key is accepted", "secret-key", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			cfg.Auth.Enabled = true
			cfg.Auth.APIKeys = []string{"secret-key"}

			rec := sendThroughChain(t, cfg, tc.token, `{}`)

			assert.Equal(t, tc.want, rec.Code)
		})
	}
}

// Auth on with no keys is a misconfiguration that would otherwise authenticate
// nobody and serve nothing: it must stop startup, not surface as 401s.
func TestBuildMiddleware_AuthEnabledWithoutKeysFailsFast(t *testing.T) {
	t.Parallel()

	cfg := validConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.APIKeys = nil

	_, err := buildMiddleware(cfg, testLogger(), httpMetrics(t), noopTelemetry(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "building authenticator")
}

// BodyLimit sits last in the chain, so an oversized body must be rejected even
// though every stage above it ran.
func TestBuildMiddleware_BodyLimitRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	cfg := validConfig()
	cfg.MaxBodyBytes = 16

	rec := sendThroughChain(t, cfg, "", `{"padding":"`+strings.Repeat("x", 512)+`"}`)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

// The optional stages must be present exactly when their flag is on — an
// always-on throttle would shed load nobody asked to shed.
func TestBuildMiddleware_OptionalStages(t *testing.T) {
	t.Parallel()

	base := validConfig()
	baseChain, err := buildMiddleware(base, testLogger(), httpMetrics(t), noopTelemetry(t))
	require.NoError(t, err)

	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantAdd int
	}{
		{"throttle adds one stage", func(c *config.Config) { c.Throttle.Enabled = true }, 1},
		{"otel adds one stage", func(c *config.Config) { c.Otel.Enabled = true }, 1},
		{"auth adds one stage", func(c *config.Config) {
			c.Auth.Enabled = true
			c.Auth.APIKeys = []string{"k"}
		}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			tc.mutate(&cfg)
			chain, err := buildMiddleware(cfg, testLogger(), httpMetrics(t), noopTelemetry(t))

			require.NoError(t, err)
			assert.Len(t, chain, len(baseChain)+tc.wantAdd)
		})
	}
}

// sendThroughChain mounts the built chain on a bare gin engine and drives one
// request through it, returning the recorded response.
func sendThroughChain(t *testing.T, cfg config.Config, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	chain, err := buildMiddleware(cfg, testLogger(), httpMetrics(t), noopTelemetry(t))
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(chain...)
	// Decode through web.BindJSON exactly as every real handler does, so the
	// oversized-body path this exercises is the production one: BodyLimit caps
	// the reader, and BindJSON is what turns the resulting MaxBytesError into a
	// 413 envelope.
	group.POST("/probe", func(c *gin.Context) {
		if _, ok := web.BindJSON[map[string]string](c); !ok {
			return
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/probe", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func httpMetrics(t *testing.T) *telemetry.HTTPMetrics {
	t.Helper()
	m, err := telemetry.NewHTTPMetrics(noopTelemetry(t).Meter)
	require.NoError(t, err)
	return m
}

// noopTelemetry builds the disabled providers through the real Init, so these
// tests exercise the same construction path a telemetry-off deployment takes.
func noopTelemetry(t *testing.T) *telemetry.Providers {
	t.Helper()
	tel, err := telemetry.Init(context.Background(), telemetry.Config{Enabled: false})
	require.NoError(t, err)
	return tel
}

// The worker's handler is injected at the composition root, so it is the one
// place a malformed broker message is decoded. It must report the failure
// rather than swallow it: internal/worker counts the failure and logs it, and a
// handler that returned nil on garbage would make a poison message invisible.
func TestNewEventHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{"well-formed event decodes", `{"type":"item.created","item_id":"abc"}`, ""},
		{"empty object decodes", `{}`, ""},
		{"malformed json is reported", `{"type":`, "decoding event"},
		{"empty payload is reported", ``, "decoding event"},
		{"wrong type for a field is reported", `{"type":123}`, "decoding event"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := newEventHandler()(context.Background(), messaging.Message{
				Topic:   eventsTopic,
				Payload: []byte(tc.payload),
			})

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
