package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
	"go.mongodb.org/mongo-driver/bson"
)

func (s *Server) listDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	query, err := parseDocumentQuery(c)
	if err != nil {
		writeError(c, err)
		return
	}

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	result, err := store.List(ctx, query)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
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

func (s *Server) createDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	var data bson.M
	if !bindJSON(c, &data) {
		return
	}

	collection, err := s.deps.CollectionStore.Get(ctx, collectionName)
	if err != nil {
		writeError(c, err)
		return
	}

	if err := collection.ValidateDocument(data); err != nil {
		writeError(c, err)
		return
	}

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	result, err := store.Create(ctx, data)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusCreated, result)
}

func (s *Server) updateDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	var data bson.M
	if !bindJSON(c, &data) {
		return
	}

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	result, err := store.Update(ctx, id, data)
	if err != nil {
		writeError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) deleteDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	store := collections.NewDocument(s.deps.MongoClient.Client, s.deps.MongoClient.Database(), collectionName)

	if err := store.Delete(ctx, id); err != nil {
		writeError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func parseDocumentQuery(c *gin.Context) (collections.DocumentQuery, error) {
	query := collections.DocumentQuery{
		Page:  1,
		Limit: 20,
	}

	if p := c.Query("page"); p != "" {
		parsed, err := strconv.ParseInt(p, 10, 64)
		if err != nil || parsed <= 0 {
			return query, newBadRequest("invalid page parameter")
		}
		query.Page = parsed
	}
	if l := c.Query("limit"); l != "" {
		parsed, err := strconv.ParseInt(l, 10, 64)
		if err != nil || parsed <= 0 {
			return query, newBadRequest("invalid limit parameter")
		}
		query.Limit = parsed
	}

	if f := c.Query("filter"); f != "" {
		var filter bson.M
		if err := json.Unmarshal([]byte(f), &filter); err != nil {
			return query, newBadRequest("invalid filter JSON")
		}
		query.Filter = filter
	}

	if so := c.Query("sort"); so != "" {
		var sort bson.M
		if err := json.Unmarshal([]byte(so), &sort); err != nil {
			return query, newBadRequest("invalid sort JSON")
		}
		query.Sort = sort
	}

	return query, nil
}
