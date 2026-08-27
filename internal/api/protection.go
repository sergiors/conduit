package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) createProtection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionSettings.EnableDeletionProtection(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

func (s *Server) deleteProtection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionSettings.DisableDeletionProtection(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
