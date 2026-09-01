package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

// Default and maximum page sizes for the documents list endpoint. These cap
// the number of documents returned per request so a single call can never
// fetch an unbounded result set.
const (
	defaultDocumentLimit = 100
	maxDocumentLimit     = 1000
)

func (s *Server) listDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	opts, err := parseDocumentListOptions(c)
	if err != nil {
		writeError(c, err)
		return
	}

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	documents, err := store.List(ctx, opts)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, documents)
}

// parseDocumentListOptions reads and validates the limit and skip query
// parameters for the documents list endpoint. limit is optional and defaults
// to defaultDocumentLimit, capped at maxDocumentLimit. skip is optional and
// defaults to zero. Malformed, zero/negative limit, or negative skip values
// are rejected as bad requests.
func parseDocumentListOptions(c *gin.Context) (collections.DocumentListOptions, error) {
	opts := collections.DocumentListOptions{
		Limit: defaultDocumentLimit,
	}

	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return opts, newBadRequest("limit must be a positive integer")
		}
		if limit <= 0 {
			return opts, newBadRequest("limit must be a positive integer")
		}
		if limit > maxDocumentLimit {
			limit = maxDocumentLimit
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

func (s *Server) getDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	doc, err := store.Get(ctx, id)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, doc)
}
