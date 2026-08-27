package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

func (s *Server) getSinks(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	sinks, err := s.deps.CollectionStore.GetSinks(ctx, name)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, sinks)
}

func (s *Server) updateSinks(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var sinks []collections.SinkConfig
	if !bindJSON(c, &sinks) {
		return
	}

	if err := s.deps.CollectionStore.UpdateSinks(ctx, name, sinks); err != nil {
		writeError(c, err)
		return
	}

	s.notifyConfigChange(ctx, name)
	c.JSON(http.StatusOK, sinks)
}
