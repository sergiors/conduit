package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

func (s *Server) getSinks(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	sinks, err := s.deps.Collections.GetSinks(ctx, name)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, sinks)
}

func (s *Server) createSink(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var sink collections.Sink
	if !bindJSON(c, &sink) {
		return
	}

	created, err := s.deps.Collections.CreateSink(ctx, name, sink)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusCreated, created)
}

func (s *Server) deleteSink(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")
	id := c.Param("id")

	if err := s.deps.Collections.DeleteSink(ctx, name, id); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// updateSink applies a partial (PATCH) update to a sink's mutable fields —
// filter and eventTypes. type and spec are immutable: attempting to
// change them returns 400 sink_identity_immutable (create a new sink instead).
func (s *Server) updateSink(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")
	id := c.Param("id")

	var update collections.SinkUpdate
	if !bindStrictJSON(c, &update) {
		return
	}

	updated, err := s.deps.Collections.UpdateSink(ctx, name, id, update)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, updated)
}
