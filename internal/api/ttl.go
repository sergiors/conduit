package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) createTTL(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var body struct {
		Attribute string `json:"attribute"`
	}
	if !bindJSON(c, &body) {
		return
	}

	if err := s.deps.Collections.SetTTL(ctx, name, body.Attribute); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

func (s *Server) deleteTTL(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.Collections.DisableTTL(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
