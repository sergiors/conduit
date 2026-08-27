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

func (s *Server) createSink(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var config collections.SinkConfig
	if !bindJSON(c, &config) {
		return
	}

	sink, err := s.deps.CollectionStore.CreateSink(ctx, name, config)
	if err != nil {
		writeError(c, err)
		return
	}

	s.publishConfigChange(ctx, name)
	c.JSON(http.StatusCreated, sink)
}

func (s *Server) deleteSink(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")
	sinkID := c.Param("sinkId")

	if err := s.deps.CollectionStore.DeleteSink(ctx, name, sinkID); err != nil {
		writeError(c, err)
		return
	}

	s.publishConfigChange(ctx, name)
	c.Status(http.StatusNoContent)
}
