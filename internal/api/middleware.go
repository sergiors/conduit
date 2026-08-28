package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// authMiddleware enforces bearer-token authentication on a route group. It
// aborts the request with a canonical 401 response unless the Authorization
// header carries exactly "Bearer <token>" and the token matches apiKey.
func authMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Config requires API_KEY, so this should never happen, but fail
		// closed rather than silently allowing unauthenticated access.
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusInternalServerError, ErrorResponse{
				Error: ErrorInfo{Code: "internal_error", Message: "server is missing an API key"},
			})
			return
		}

		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{Code: "unauthorized", Message: "missing authorization header"},
			})
			return
		}

		// Scheme is case-sensitive and must be followed by exactly one space.
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{Code: "unauthorized", Message: "invalid authorization scheme, expected Bearer"},
			})
			return
		}

		token := strings.TrimPrefix(header, prefix)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{Code: "unauthorized", Message: "invalid token"},
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorInfo{Code: "unauthorized", Message: "invalid token"},
			})
			return
		}

		c.Next()
	}
}
