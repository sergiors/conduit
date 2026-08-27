package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

func (s *Server) listDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	documents, err := store.List(ctx)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, documents)
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
