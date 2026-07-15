// pagination.go
package http

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/luisfelipecoelho/go-template/internal/web"
)

// parsePage reads and validates the page/rows query parameters, defaulting to
// page 1 / 20 rows. Out-of-range values are rejected with 400 (no silent
// clamping); the domain enforces the upper bounds.
func parsePage(c *gin.Context) (page, rows int, ok bool) {
	page, ok = parseIntQuery(c, "page", 1)
	if !ok {
		return 0, 0, false
	}
	rows, ok = parseIntQuery(c, "rows", 20)
	if !ok {
		return 0, 0, false
	}
	return page, rows, true
}

func parseIntQuery(c *gin.Context, name string, def int) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return def, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		web.Error(c, http.StatusBadRequest, web.CodeValidation, name+" must be an integer")
		return 0, false
	}
	return v, true
}
