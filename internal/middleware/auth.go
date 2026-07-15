// auth.go
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/luisfelipecoelho/go-template/internal/web"
)

// Authenticator validates a bearer token from the Authorization header. It is
// owned here (the consumer) so the server carries no dependency on any concrete
// auth scheme; implementations (static API keys, JWT, OIDC) live in
// internal/domain/auth and are injected by the composition root.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) error
}

const bearerPrefix = "Bearer "

// Auth rejects any request without a valid bearer token. Include it in the
// chain only when authentication is required. On failure it returns 401 with
// the standard envelope and a WWW-Authenticate challenge — never echoing why,
// so a probe cannot distinguish "missing" from "wrong".
func Auth(a Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := bearerToken(c)
		if !ok {
			c.Header("WWW-Authenticate", "Bearer")
			web.Error(c, http.StatusUnauthorized, web.CodeUnauthorized, "authentication required")
			return
		}
		if err := a.Authenticate(c.Request.Context(), token); err != nil {
			c.Header("WWW-Authenticate", "Bearer")
			web.Error(c, http.StatusUnauthorized, web.CodeUnauthorized, "authentication required")
			return
		}
		c.Next()
	}
}

// bearerToken extracts a non-empty token from an "Authorization: Bearer <token>"
// header. The scheme match is case-insensitive per RFC 7235; the token is not.
func bearerToken(c *gin.Context) (string, bool) {
	h := c.GetHeader("Authorization")
	if len(h) <= len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}
