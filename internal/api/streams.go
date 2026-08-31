package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) createStream(c *gin.Context) {
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

	if err := s.deps.CollectionSettings.EnableStream(ctx, name, *body.OldImage); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusCreated)
}

func (s *Server) deleteStream(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionSettings.DisableStream(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
