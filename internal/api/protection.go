package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) enableProtection(c *gin.Context) {
	s.setProtection(c, true)
}

func (s *Server) disableProtection(c *gin.Context) {
	s.setProtection(c, false)
}

func (s *Server) setProtection(c *gin.Context, enabled bool) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionStore.SetDeletionProtection(ctx, name, enabled); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
