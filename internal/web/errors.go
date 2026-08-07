// errors.go
package web

import "github.com/gin-gonic/gin"

// Error codes for the machine-readable `error` field of the envelope. All are
// exported: domain adapters map their sentinels to the first three, and the
// middleware package produces the rest.
const (
	CodeValidation   = "validation_error"
	CodeNotFound     = "not_found"
	CodeInternal     = "internal_error"
	CodeTooLarge     = "payload_too_large"
	CodeUnauthorized = "unauthorized"
	CodeOverloaded   = "overloaded"
	CodeTimeout      = "timeout"

	// GenericInternal is the client-facing 500 message — it never leaks internals.
	GenericInternal = "internal error"
)

// Error writes the standard error envelope with the request id and aborts the
// chain. The message is caller-controlled and must never contain internal
// detail. Domain HTTP adapters and middleware call this to render responses.
func Error(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error:     code,
		Message:   message,
		RequestID: RequestIDFromContext(c),
	})
}
