package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

// Default and maximum page sizes for the DLQ list endpoint. These cap the
// number of entries returned per request so a single call can never fetch an
// unbounded result set.
const (
	defaultDLQLimit = 100
	maxDLQLimit     = 1000
)

func (s *Server) listDLQ(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	opts, err := parseDLQListOptions(c)
	if err != nil {
		writeError(c, err)
		return
	}

	// The manager validates the collection is managed before reading the DLQ.
	// Unknown or unmanaged physical collections return 404 and never touch
	// config.dlq.
	entries, err := s.deps.Collections.ListDLQEntries(ctx, collectionName, opts)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, entries)
}

// parseDLQListOptions reads and validates the limit and skip query parameters
// for the DLQ list endpoint. limit is optional and defaults to
// defaultDLQLimit, capped at maxDLQLimit. skip is optional and defaults to
// zero. Malformed, zero/negative limit, or negative skip values are rejected
// as bad requests.
func parseDLQListOptions(c *gin.Context) (collections.DLQListOptions, error) {
	opts := collections.DLQListOptions{
		Limit: defaultDLQLimit,
	}

	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return opts, newBadRequest("limit must be a positive integer")
		}
		if limit <= 0 {
			return opts, newBadRequest("limit must be a positive integer")
		}
		if limit > maxDLQLimit {
			limit = maxDLQLimit
		}
		opts.Limit = limit
	}

	if raw := c.Query("skip"); raw != "" {
		skip, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return opts, newBadRequest("skip must be a non-negative integer")
		}
		if skip < 0 {
			return opts, newBadRequest("skip must be a non-negative integer")
		}
		opts.Skip = skip
	}

	return opts, nil
}

func (s *Server) getDLQ(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	// The manager validates the collection is managed before reading the DLQ.
	entry, err := s.deps.Collections.GetDLQEntry(ctx, collectionName, id)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, entry)
}
