package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) enableTTL(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var body struct {
		Attribute string `json:"attribute"`
	}
	if !bindJSON(c, &body) {
		return
	}

	if err := s.deps.CollectionSettings.SetTTL(ctx, name, body.Attribute); err != nil {
		writeError(c, err)
		return
	}

	s.publishConfigChange(ctx, name)
	c.Status(http.StatusNoContent)
}

func (s *Server) disableTTL(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionSettings.DisableTTL(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	s.publishConfigChange(ctx, name)
	c.Status(http.StatusNoContent)
}
