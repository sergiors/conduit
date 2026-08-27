package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBindStrictJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("accepts known fields", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"collection_name":"users","partition_key":"id","sort_key":"sk"}`))

		var req CreateCollectionRequest
		assert.True(t, bindStrictJSON(c, &req))
		assert.Equal(t, "users", req.CollectionName)
		assert.Equal(t, "id", req.PartitionKey)
		assert.Equal(t, "sk", req.SortKey)
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"collection_name":"users","stream_enabled":true}`))

		var req CreateCollectionRequest
		assert.False(t, bindStrictJSON(c, &req))
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"invalid_request"`)
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{invalid`))

		var req CreateCollectionRequest
		assert.False(t, bindStrictJSON(c, &req))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
