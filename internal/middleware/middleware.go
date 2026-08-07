// Package middleware is a set of composable gin middlewares for the HTTP server.
// Each is an exported constructor returning a gin.HandlerFunc; the composition
// root assembles them into the /v1 chain (order matters) and passes them to
// web.NewServer via WithGroupMiddleware. This keeps the server a pure engine
// and makes the middleware the reusable, testable unit.
//
// To add a middleware: add a constructor here returning gin.HandlerFunc, then
// insert it into the chain in the composition root at the position you need.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/luisfelipecoelho/go-template/internal/web"
	"github.com/luisfelipecoelho/go-template/pkg/uid"
)

const headerRequestID = "X-Request-ID"

// RequestID assigns each request a correlation id: it honours an inbound
// X-Request-ID or generates a UUID, stores it for the rest of the chain, and
// echoes it on the response. Put it first so the id is available to every layer.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if id == "" {
			generated, err := uid.New()
			if err != nil {
				// The system CSPRNG is unavailable — fail secure rather than
				// serve a request that cannot be correlated.
				web.Error(c, http.StatusInternalServerError, web.CodeInternal, web.GenericInternal)
				return
			}
			id = generated
		}
		web.StoreRequestID(c, id)
		c.Header(headerRequestID, id)
		c.Next()
	}
}

// SecurityHeaders sets conservative response headers on every request. The
// values suit a JSON API: nothing here is meant to be rendered, framed, or
// cached, so each header denies rather than configures. HSTS is deliberately
// absent — TLS terminates at the ingress, which owns that header.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		// An API serves no scripts, styles, images, or frames. Locking the
		// policy to 'none' means a response that somehow renders as HTML (a
		// proxy error page, a future non-JSON route) still executes nothing.
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		// Never leak the request URL — it can carry ids — to a third-party host.
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

// BodyLimit caps the request body size, turning oversized bodies into a 413
// (surfaced by web.BindJSON).
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// Metrics records one datapoint per HTTP response. The concrete OTel
// implementation lives in internal/telemetry; this interface is owned here (the
// consumer) so the server has no OTel dependency.
type Metrics interface {
	RecordRequest(ctx context.Context, method, route string, status int, dur time.Duration)
}

// Telemetry records a metric for every response and logs ONLY on 5xx
// (errors-only policy, ADR-6). The route template — not the raw path — is used
// as the metric attribute to bound cardinality. Place it high in the chain so
// it observes every response below it, including 401/429/503 and recovered 500s.
func Telemetry(log *slog.Logger, m Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := c.Writer.Status()
		dur := time.Since(start)
		m.RecordRequest(c.Request.Context(), c.Request.Method, route, status, dur)

		if status >= http.StatusInternalServerError {
			log.ErrorContext(c.Request.Context(), "request failed",
				slog.String("method", c.Request.Method),
				slog.String("route", route),
				slog.Int("status", status),
				slog.String("request_id", web.RequestIDFromContext(c)),
				slog.Duration("duration", dur),
			)
		}
	}
}

// Recovery converts a panic into a 500 envelope, logging the panic without
// leaking it to the client.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, err any) {
		log.ErrorContext(c.Request.Context(), "panic recovered",
			slog.String("request_id", web.RequestIDFromContext(c)),
			slog.Any("panic", err),
		)
		web.Error(c, http.StatusInternalServerError, web.CodeInternal, web.GenericInternal)
	})
}
