package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardLogger returns a logger that drops output; tests assert behaviour, not logs.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// echoRoutes is a domain-free RouteRegister: /ping returns 200, /echo decodes a
// body via BindJSON (exercising the 400/413 paths).
type echoRoutes struct{}

func (echoRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	rg.POST("/echo", func(c *gin.Context) {
		if _, ok := BindJSON[map[string]any](c); ok {
			c.Status(http.StatusOK)
		}
	})
}

func newServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	log := discardLogger()
	ready := NewReadiness()
	ready.SetReady(true)
	all := append([]Option{WithRoutes(echoRoutes{})}, opts...)
	return NewServer(Config{Env: "prod", Version: "test"}, log, ready, all...)
}

func req(s *Server, method, path, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec
}

func TestRouting_AndHealth(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	assert.Equal(t, http.StatusOK, req(s, http.MethodGet, "/v1/ping", "").Code)
	assert.Equal(t, http.StatusOK, req(s, http.MethodGet, "/healthz", "").Code)
	assert.Equal(t, http.StatusOK, req(s, http.MethodGet, "/readyz", "").Code)
}

func TestReadyz_503WhenNotReady(t *testing.T) {
	t.Parallel()
	log := discardLogger()
	ready := NewReadiness() // not ready
	s := NewServer(Config{Env: "prod"}, log, ready, WithRoutes(echoRoutes{}))
	rec := req(s, http.MethodGet, "/readyz", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "not_ready")
}

func TestBindJSON_Errors(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	// unknown field / malformed → 400 validation
	rec := req(s, http.MethodPost, "/v1/echo", `{"x":1,"y":2`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var env ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, CodeValidation, env.Error)
}

func TestStartStop_GracefulDrain(t *testing.T) {
	t.Parallel()
	log := discardLogger()
	ready := NewReadiness()
	s := NewServer(Config{Port: 0, Env: "prod"}, log, ready, WithRoutes(echoRoutes{}))

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- s.Start(ctx) }()
	require.Eventually(t, ready.Ready, time.Second, 10*time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	require.NoError(t, s.Stop(stopCtx))
	assert.False(t, ready.Ready())

	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}
