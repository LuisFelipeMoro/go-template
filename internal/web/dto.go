// dto.go
package web

// ErrorResponse is the single error envelope for every 4xx/5xx across all
// domains. Domain HTTP adapters render it via Error.
type ErrorResponse struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// healthResponse is returned by /healthz and /readyz.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}
