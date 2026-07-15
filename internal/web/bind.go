// bind.go
package web

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// BindJSON decodes the request body strictly into a value of the caller's
// concrete type and writes the correct error envelope on failure. It uses the
// Go 1.26 encoding/json/v2 API: UnmarshalRead streams from the body and
// RejectUnknownMembers rejects unknown fields. The bool is false when the chain
// was aborted. Oversized bodies (from the bodyLimit middleware) map to 413.
// Generic over the request type so no caller handles an untyped decode sink;
// domain handlers reuse it.
func BindJSON[T any](c *gin.Context) (T, bool) {
	var dst T
	if err := jsonv2.UnmarshalRead(c.Request.Body, &dst, jsonv2.RejectUnknownMembers(true)); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			Error(c, http.StatusRequestEntityTooLarge, CodeTooLarge, "request body too large")
			return dst, false
		}
		Error(c, http.StatusBadRequest, CodeValidation, "malformed JSON body")
		return dst, false
	}
	return dst, true
}
