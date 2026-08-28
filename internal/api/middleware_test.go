package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware(t *testing.T) {
	apiKey := "s3cr3t-token"

	setup := func() *gin.Engine {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		api := r.Group("/api", authMiddleware(apiKey))
		api.GET("/collections", func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
		return r
	}

	cases := []struct {
		name        string
		authHeader  string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"missing authorization header", "", http.StatusUnauthorized, "unauthorized", "missing authorization header"},
		{"wrong scheme", "Basic dXNlcjpwYXNz", http.StatusUnauthorized, "unauthorized", "invalid authorization scheme, expected Bearer"},
		{"lowercase scheme", "bearer s3cr3t-token", http.StatusUnauthorized, "unauthorized", "invalid authorization scheme, expected Bearer"},
		{"Bearer with no token", "Bearer", http.StatusUnauthorized, "unauthorized", "invalid authorization scheme, expected Bearer"},
		{"Bearer with empty token", "Bearer ", http.StatusUnauthorized, "unauthorized", "invalid token"},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized, "unauthorized", "invalid token"},
		{"correct token", "Bearer " + apiKey, http.StatusNoContent, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := setup()
			req := httptest.NewRequest(http.MethodGet, "/api/collections", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code, "status should match")

			if tc.wantCode != "" {
				var body ErrorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				assert.Equal(t, tc.wantCode, body.Error.Code, "error code should match")
				assert.Equal(t, tc.wantMessage, body.Error.Message, "error message should match")
			}
		})
	}
}
