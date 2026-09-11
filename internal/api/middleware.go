package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"conduit/internal/apikey"
)

// authMiddleware enforces bearer-token authentication on a route group using a
// persisted API key manager. It aborts with a canonical 401 unless the
// Authorization header carries exactly "Bearer <key>" and the key is a valid,
// currently-active API key. All failures that do not involve a valid active key
// (missing header, wrong scheme, unknown, malformed, or revoked key) return the
// same 401 "invalid token" family of responses so no information leaks about
// which keys exist.
//
// A nil manager or an infrastructure failure (e.g. MongoDB unreachable) fails
// closed with a 500 so an unauthenticated request can never succeed.
func authMiddleware(keys *apikey.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{
					Code:    "unauthorized",
					Message: "missing authorization header",
				},
			})
			return
		}

		// Scheme is case-sensitive and must be followed by exactly one space.
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{
					Code:    "unauthorized",
					Message: "invalid authorization scheme, expected Bearer",
				},
			})
			return
		}

		token := strings.TrimPrefix(header, prefix)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{
					Code:    "unauthorized",
					Message: "invalid token",
				},
			})
			return
		}

		// A nil manager means the server failed to wire authentication; fail
		// closed rather than silently allowing unauthenticated access.
		if keys == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
				Error: ErrorInfo{
					Code:    "internal_error",
					Message: "server is missing an API key",
				},
			})
			return
		}

		valid, err := keys.Authenticate(c.Request.Context(), token)
		if err != nil {
			// Infrastructure failure: fail closed rather than letting an
			// unverifiable request through.
			c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
				Error: ErrorInfo{
					Code:    "internal_error",
					Message: "server is missing an API key",
				},
			})
			return
		}
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{
					Code:    "unauthorized",
					Message: "invalid token",
				},
			})
			return
		}

		c.Next()
	}
}
