package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/apikey"
)

// newAuthRouter builds a Gin router protected by the API key middleware, with
// a no-op handler so requests reach authentication before anything else.
func newAuthRouter(keys *apikey.Manager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api", authMiddleware(keys))
	api.GET("/collections", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return r
}

// TestAuthMiddleware_HeaderAndScheme validates the header parsing rules that do
// not require MongoDB: missing header, wrong/lowercase scheme, and Bearer with
// no token all produce the canonical 401 responses. These are table-tested with
// a nil manager because the failure happens before any lookup — but a nil
// manager must fail CLOSED for the 500 path, so the nil case is asserted
// separately.
func TestAuthMiddleware_HeaderAndScheme(t *testing.T) {
	cases := []struct {
		name        string
		authHeader  string
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{"missing authorization header", "", http.StatusUnauthorized, "unauthorized", "missing authorization header"},
		{"wrong scheme", "Basic dXNlcjpwYXNz", http.StatusUnauthorized, "unauthorized", "invalid authorization scheme, expected Bearer"},
		{"lowercase scheme", "bearer sk-test-token", http.StatusUnauthorized, "unauthorized", "invalid authorization scheme, expected Bearer"},
		{"Bearer with no token", "Bearer", http.StatusUnauthorized, "unauthorized", "invalid authorization scheme, expected Bearer"},
		{"Bearer with empty token", "Bearer ", http.StatusUnauthorized, "unauthorized", "invalid token"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newAuthRouter(nil)
			req := httptest.NewRequest(http.MethodGet, "/api/collections", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantStatus, rec.Code, "status should match")
			var body ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.wantCode, body.Error.Code, "error code should match")
			assert.Equal(t, tc.wantMessage, body.Error.Message, "error message should match")
		})
	}
}

// TestAuthMiddleware_NilManagerFailsClosed asserts that a nil manager produces
// a 500 and never lets an unauthenticated request through.
func TestAuthMiddleware_NilManagerFailsClosed(t *testing.T) {
	r := newAuthRouter(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/collections", nil)
	req.Header.Set("Authorization", "Bearer sk-anything")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Equal(t, "server is missing an API key", body.Error.Message)
}

// TestAuthMiddleware_KeyValidity is an integration-style test that exercises a
// persisted key: a valid active key passes, an unknown/malformed key is
// rejected, and a revoked key is rejected. It needs MongoDB to wire a manager,
// so it is skipped when MongoDB is unavailable.
func TestAuthMiddleware_KeyValidity(t *testing.T) {
	server, _, _, _ := newMongoTestServer(t, "conduit_test_auth_middleware")
	keys := server.deps.APIKeys

	// newMongoTestServer created a valid key; its plaintext is in testToken.
	ok, err := keys.Authenticate(context.Background(), testToken)
	require.NoError(t, err)
	assert.True(t, ok)

	// A well-formed-but-unknown token is rejected with a 401, not a 500.
	unknown := "sk-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 43 chars of A
	rec := hit(server, "Bearer "+unknown)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "unknown key must be rejected")

	// A malformed token is rejected.
	rec = hit(server, "Bearer not-a-key")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "malformed key must be rejected")

	// Revoke the test key, then it must no longer authenticate. Find its id by
	// name through the manager's List (records carry IDs and names).
	listed, err := keys.List(context.Background(), 0)
	require.NoError(t, err)
	var testID string
	for _, k := range listed {
		if k.Name == "test" {
			testID = k.ID
		}
	}
	require.NotEmpty(t, testID, "test key must be present in list")
	require.NoError(t, keys.Revoke(context.Background(), testID))

	rec = hit(server, "Bearer "+testToken)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "revoked key must be rejected")
}

// hit issues a GET against the API with the given Authorization header.
func hit(server *Server, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/collections", nil)
	req.Header.Set("Authorization", authorization)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	return rec
}
