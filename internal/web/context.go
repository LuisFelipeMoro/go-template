// context.go — request-id plumbing shared by the core and the middleware
// package: the requestID middleware stores the id, and Error / telemetry read
// it back. Kept in the core so the error envelope can stamp a request id
// without importing the middleware package (which would cycle).
package web

import "github.com/gin-gonic/gin"

const ctxKeyRequestID = "request_id"

// StoreRequestID records the correlation id on the gin context. The requestID
// middleware calls this once per request.
func StoreRequestID(c *gin.Context, id string) {
	c.Set(ctxKeyRequestID, id)
}

// RequestIDFromContext returns the correlation id stored by the requestID
// middleware, or "" if none was set.
func RequestIDFromContext(c *gin.Context) string {
	if v, ok := c.Get(ctxKeyRequestID); ok {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}
