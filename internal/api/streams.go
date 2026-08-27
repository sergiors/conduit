package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) enableStream(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var body struct {
		OldImage *bool `json:"old_image"`
	}
	if !bindJSON(c, &body) {
		return
	}
	if body.OldImage == nil {
		writeError(c, newBadRequest("old_image is required"))
		return
	}

	if err := s.deps.CollectionStore.SetStream(ctx, name, *body.OldImage); err != nil {
		writeError(c, err)
		return
	}

	s.notifyConfigChange(ctx, name)
	c.Status(http.StatusNoContent)
}

func (s *Server) disableStream(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionStore.DisableStream(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	s.notifyConfigChange(ctx, name)
	c.Status(http.StatusNoContent)
}
