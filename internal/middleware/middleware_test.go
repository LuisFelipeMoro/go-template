package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/middleware"
	"github.com/luisfelipecoelho/go-template/internal/web"
)

type recordingMetrics struct {
	mu    sync.Mutex
	calls int
	last  int
}

func (m *recordingMetrics) RecordRequest(_ context.Context, _, _ string, status int, _ time.Duration) {
	m.mu.Lock()
	m.calls++
	m.last = status
	m.mu.Unlock()
}

type testRoutes struct{}

func (testRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	rg.GET("/boom", func(_ *gin.Context) { panic("boom") })
	// /slow reports whether its request context was cancelled before the work
	// finished — the observable effect Timeout is responsible for.
	rg.GET("/slow", func(c *gin.Context) {
		select {
		case <-c.Request.Context().Done():
			c.JSON(http.StatusOK, gin.H{"cancelled": true})
		case <-time.After(2 * time.Second):
			c.JSON(http.StatusOK, gin.H{"cancelled": false})
		}
	})
}

type stubAuth struct{ valid string }

func (s stubAuth) Authenticate(_ context.Context, token string) error {
	if token == s.valid {
		return nil
	}
	return errors.New("nope")
}

type fixedLimiter struct{ ok bool }

func (f fixedLimiter) Acquire() bool { return f.ok }
func (fixedLimiter) Release()        {}

func server(t *testing.T, buf *bytes.Buffer, chain ...gin.HandlerFunc) *web.Server {
	t.Helper()
	if buf == nil {
		buf = &bytes.Buffer{}
	}
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelError}))
	ready := web.NewReadiness()
	ready.SetReady(true)
	return web.NewServer(web.Config{Env: "prod"}, log, ready,
		web.WithGroupMiddleware(chain...), web.WithRoutes(testRoutes{}))
}

func get(s *web.Server, path, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRequestID_AndSecurityHeaders(t *testing.T) {
	t.Parallel()
	s := server(t, nil, middleware.RequestID(), middleware.SecurityHeaders())
	rec := get(s, "/v1/ping", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
}

func TestTelemetry_MetricAndNoLogOn2xx(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	m := &recordingMetrics{}
	s := server(t, &buf, middleware.RequestID(), middleware.Telemetry(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})), m))
	get(s, "/v1/ping", "")
	assert.Equal(t, 1, m.calls)
	assert.Equal(t, http.StatusOK, m.last)
	assert.Empty(t, buf.String(), "ADR-6: no log on 2xx")
}

func TestRecovery_AndTelemetryLogsOn5xx(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	m := &recordingMetrics{}
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	s := server(t, &buf, middleware.RequestID(), middleware.Telemetry(log, m), middleware.Recovery(log))
	rec := get(s, "/v1/boom", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), web.CodeInternal)
	assert.NotContains(t, rec.Body.String(), "boom", "panic must not leak")
	assert.Equal(t, http.StatusInternalServerError, m.last)
	assert.Contains(t, buf.String(), `"level":"ERROR"`)
}

func TestAuth(t *testing.T) {
	t.Parallel()
	s := server(t, nil, middleware.RequestID(), middleware.Auth(stubAuth{valid: "secret"}))
	assert.Equal(t, http.StatusUnauthorized, get(s, "/v1/ping", "").Code)
	assert.Equal(t, http.StatusUnauthorized, get(s, "/v1/ping", "Bearer wrong").Code)
	assert.Equal(t, http.StatusOK, get(s, "/v1/ping", "Bearer secret").Code)
	assert.Equal(t, http.StatusOK, get(s, "/v1/ping", "bearer secret").Code)
}

func TestThrottle(t *testing.T) {
	t.Parallel()
	busy := server(t, nil, middleware.RequestID(), middleware.Throttle(fixedLimiter{ok: false}))
	rec := get(busy, "/v1/ping", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))

	free := server(t, nil, middleware.RequestID(), middleware.Throttle(fixedLimiter{ok: true}))
	assert.Equal(t, http.StatusOK, get(free, "/v1/ping", "").Code)
}

func TestSemaphore(t *testing.T) {
	t.Parallel()
	sem := middleware.NewSemaphore(1)
	require.True(t, sem.Acquire())
	assert.False(t, sem.Acquire())
	sem.Release()
	assert.True(t, sem.Acquire())
}

func TestBodyLimit(t *testing.T) {
	t.Parallel()
	// BodyLimit wraps the body; oversize is surfaced by web.BindJSON as 413.
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ready := web.NewReadiness()
	ready.SetReady(true)
	s := web.NewServer(web.Config{Env: "prod"}, log, ready,
		web.WithGroupMiddleware(middleware.RequestID(), middleware.BodyLimit(4)),
		web.WithRoutes(bodyRoute{}))
	req := httptest.NewRequest(http.MethodPost, "/v1/big", io.NopCloser(bytes.NewBufferString(`{"aaaa":"bbbb"}`)))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

type bodyRoute struct{}

func (bodyRoute) Register(rg *gin.RouterGroup) {
	rg.POST("/big", func(c *gin.Context) {
		if _, ok := web.BindJSON[map[string]any](c); ok {
			c.Status(http.StatusOK)
		}
	})
}

func TestTimeout_CancelsHandlerContext(t *testing.T) {
	t.Parallel()
	s := server(t, nil, middleware.Timeout(20*time.Millisecond))

	rec := get(s, "/v1/slow", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"cancelled":true}`, rec.Body.String(),
		"handler context must be cancelled at the deadline so downstream work stops")
}

func TestTimeout_LeavesFastHandlersUntouched(t *testing.T) {
	t.Parallel()
	s := server(t, nil, middleware.Timeout(time.Minute))

	rec := get(s, "/v1/ping", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"ok":true}`, rec.Body.String(), "a generous deadline must not alter the response")
}

func TestTimeout_DeadlineIsSetOnRequestContext(t *testing.T) {
	t.Parallel()
	var (
		hasDeadline bool
		deadline    time.Time
	)
	probe := func(c *gin.Context) {
		deadline, hasDeadline = c.Request.Context().Deadline()
		c.Next()
	}
	s := server(t, nil, middleware.Timeout(time.Minute), probe)

	require.Equal(t, http.StatusOK, get(s, "/v1/ping", "").Code)
	require.True(t, hasDeadline, "Timeout must install a deadline on the request context")
	assert.WithinDuration(t, time.Now().Add(time.Minute), deadline, 5*time.Second)
}
