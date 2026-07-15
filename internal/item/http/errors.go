// errors.go
package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/luisfelipecoelho/go-template/internal/item"
	"github.com/luisfelipecoelho/go-template/internal/web"
)

// mapDomainError maps an item domain error to the correct status + envelope.
// Internal failures collapse to a generic 500 so implementation detail never
// reaches the client; the caller logs the real error.
func mapDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, item.ErrInvalidArgument):
		// Strip the trailing sentinel so the client sees the field detail
		// ("name must be 1..120 chars") without the internal ": invalid argument".
		msg := strings.TrimSuffix(err.Error(), ": "+item.ErrInvalidArgument.Error())
		web.Error(c, http.StatusBadRequest, web.CodeValidation, msg)
	case errors.Is(err, item.ErrNotFound):
		web.Error(c, http.StatusNotFound, web.CodeNotFound, "item not found")
	default:
		web.Error(c, http.StatusInternalServerError, web.CodeInternal, web.GenericInternal)
	}
}
