package api

import (
	"encoding/json"

	"github.com/gin-gonic/gin"
)

// bindJSON parses the request body into v. On failure it writes a canonical
// 400 response and returns false.
func bindJSON(c *gin.Context, v interface{}) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		writeError(c, newBadRequest("invalid request body"))
		return false
	}
	return true
}

// bindStrictJSON parses the request body into v, rejecting any field that is
// not present in v. On failure it writes a canonical 400 response and returns
// false.
func bindStrictJSON(c *gin.Context, v interface{}) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeError(c, newBadRequest("invalid request body: %v", err))
		return false
	}
	return true
}
