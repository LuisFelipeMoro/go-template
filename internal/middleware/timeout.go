// timeout.go
package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Timeout bounds how long a handler may run by replacing the request context
// with a deadline-bounded one. Every downstream call that honours ctx — the
// store, the cache, an outbound HTTP request — is cancelled at the deadline, so
// a stuck dependency releases its goroutine and its throttle slot instead of
// occupying them indefinitely.
//
// This is not the same guarantee as http.Server's WriteTimeout. That bound is
// enforced by the transport, which simply severs the connection; the handler
// goroutine keeps running and keeps holding whatever it holds. Cancelling the
// context is what actually stops the work.
//
// Timeout deliberately writes no response of its own. Doing so would race a
// handler that has already begun writing. The expired deadline instead surfaces
// as context.DeadlineExceeded through the normal error path, and each domain's
// error mapper renders it (see internal/item/http.mapDomainError → 504).
//
// Set the duration below HTTP_WRITE_TIMEOUT, so the handler returns a proper
// error envelope before the transport cuts the connection.
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
