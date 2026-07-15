// health.go
package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// healthHandler serves the liveness and readiness probes. These routes bypass
// the request-id/telemetry chain to keep probe traffic out of metrics + logs.
type healthHandler struct {
	ready   *Readiness
	version string
}

// healthz is operationId getHealth: 200 while the process is alive.
func (h *healthHandler) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, healthResponse{Status: "ok", Version: h.version})
}

// readyz is operationId getReadiness: 200 when ready, 503 while starting or
// draining.
func (h *healthHandler) readyz(c *gin.Context) {
	if !h.ready.Ready() {
		c.JSON(http.StatusServiceUnavailable, healthResponse{Status: "not_ready", Version: h.version})
		return
	}
	c.JSON(http.StatusOK, healthResponse{Status: "ok", Version: h.version})
}
