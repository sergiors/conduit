package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDocumentListOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(query string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/documents?"+query, nil)
		return c
	}

	t.Run("defaults when no params", func(t *testing.T) {
		opts, err := parseDocumentListOptions(newCtx(""))
		require.NoError(t, err)
		assert.Equal(t, int64(defaultDocumentLimit), opts.Limit)
		assert.Equal(t, int64(0), opts.Skip)
	})

	t.Run("accepts explicit limit and skip", func(t *testing.T) {
		opts, err := parseDocumentListOptions(newCtx("limit=50&skip=10"))
		require.NoError(t, err)
		assert.Equal(t, int64(50), opts.Limit)
		assert.Equal(t, int64(10), opts.Skip)
	})

	t.Run("caps limit above max", func(t *testing.T) {
		opts, err := parseDocumentListOptions(newCtx("limit=5000"))
		require.NoError(t, err)
		assert.Equal(t, int64(maxDocumentLimit), opts.Limit)
	})

	t.Run("accepts skip without limit", func(t *testing.T) {
		opts, err := parseDocumentListOptions(newCtx("skip=3"))
		require.NoError(t, err)
		assert.Equal(t, int64(defaultDocumentLimit), opts.Limit)
		assert.Equal(t, int64(3), opts.Skip)
	})

	t.Run("rejects malformed limit", func(t *testing.T) {
		_, err := parseDocumentListOptions(newCtx("limit=abc"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})

	t.Run("rejects zero limit", func(t *testing.T) {
		_, err := parseDocumentListOptions(newCtx("limit=0"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})

	t.Run("rejects negative limit", func(t *testing.T) {
		_, err := parseDocumentListOptions(newCtx("limit=-5"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})

	t.Run("rejects malformed skip", func(t *testing.T) {
		_, err := parseDocumentListOptions(newCtx("skip=xyz"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})

	t.Run("rejects negative skip", func(t *testing.T) {
		_, err := parseDocumentListOptions(newCtx("skip=-1"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})
}
