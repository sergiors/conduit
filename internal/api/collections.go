package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
)

func (s *Server) listCollections(c *gin.Context) {
	ctx := c.Request.Context()

	list, err := s.deps.CollectionStore.List(ctx)
	if err != nil {
		writeError(c, err)
		return
	}

	if list == nil {
		list = []collections.Collection{}
	}

	c.JSON(http.StatusOK, list)
}

// CreateCollectionRequest is the minimal payload accepted when creating a
// collection. Optional features are configured through dedicated endpoints.
type CreateCollectionRequest struct {
	CollectionName string `json:"collection_name"`
	PartitionKey   string `json:"partition_key"`
	SortKey        string `json:"sort_key"`
}

func (s *Server) createCollection(c *gin.Context) {
	ctx := c.Request.Context()

	var req CreateCollectionRequest
	if !bindStrictJSON(c, &req) {
		return
	}

	collection := collections.Collection{
		CollectionName: req.CollectionName,
		PartitionKey:   req.PartitionKey,
		SortKey:        req.SortKey,
	}

	if err := s.deps.CollectionStore.Create(ctx, &collection); err != nil {
		writeError(c, err)
		return
	}

	s.publishConfigChange(ctx, collection.CollectionName)
	c.JSON(http.StatusCreated, collection)
}

func (s *Server) getCollection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	collection, err := s.deps.CollectionStore.Get(ctx, name)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, collection)
}

func (s *Server) deleteCollection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	if err := s.deps.CollectionStore.Delete(ctx, name); err != nil {
		writeError(c, err)
		return
	}

	s.publishConfigChange(ctx, name)
	c.Status(http.StatusNoContent)
}
