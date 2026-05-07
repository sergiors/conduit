package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/collections"
	"go.mongodb.org/mongo-driver/bson"
)

type Server struct {
	collectionStore  *collections.Store
	mongoClient *mongo.Client
	redisClient *redis.Client
}

func NewServer() (*Server, error) {
	// Get config from environment (REQUIRED - no defaults)
	uri := getRequiredEnv("MONGODB_URI")
	database := getRequiredEnv("MONGODB_DATABASE")
	redisURI := getRequiredEnv("REDIS_URI")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.NewClient(ctx, mongo.Config{
		URI:      uri,
		Database: database,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	store := collections.NewStore(client.Client, database)
	if err := store.CreateIndex(ctx); err != nil {
		return nil, err
	}

	redisClient, err := redis.NewClient(ctx, redis.Config{
		URI:    redisURI,
		Prefix: "cdc:",
	})
	if err != nil {
		client.Close(ctx)
		return nil, err
	}

	return &Server{
		collectionStore:  store,
		mongoClient: client,
		redisClient: redisClient,
	}, nil
}

func (s *Server) Close(ctx context.Context) error {
	if err := s.redisClient.Close(); err != nil {
		return err
	}
	return s.mongoClient.Close(ctx)
}

func main() {
	server, err := NewServer()
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	defer server.Close(context.Background())

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// Collection configuration (control plane)
	router.GET("/api/collections", server.listCollections)
	router.POST("/api/collections", server.createCollection)
	router.GET("/api/collections/:name", server.getCollection)
	router.PUT("/api/collections/:name", server.updateCollection)
	router.DELETE("/api/collections/:name", server.deleteCollection)

	// Collection documents CRUD (data plane)
	router.GET("/api/collections/:name/documents", server.listDocuments)
	router.POST("/api/collections/:name/documents", server.createDocument)
	router.PUT("/api/collections/:name/documents", server.updateDocument)
	router.DELETE("/api/collections/:name/documents", server.deleteDocument)

	router.GET("/health", server.handleHealth)

	port := getEnv("PORT", "8080")
	log.Printf("API server starting on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func (s *Server) listCollections(c *gin.Context) {
	ctx := c.Request.Context()
	collectionList, err := s.collectionStore.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if collectionList == nil {
		collectionList = []collections.Collection{}
	}

	c.JSON(http.StatusOK, collectionList)
}

func (s *Server) getCollection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	collection, err := s.collectionStore.Get(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}

	c.JSON(http.StatusOK, collection)
}

func (s *Server) createCollection(c *gin.Context) {
	ctx := c.Request.Context()

	var collection collections.Collection
	if err := c.ShouldBindJSON(&collection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	collectionName := collection.CollectionName
	if collectionName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Collection name is required"})
		return
	}
	collection.CollectionName = collectionName
	if collection.SortKey != "" && collection.PrimaryKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "primary_key is required when sort_key is defined"})
		return
	}
	if collection.PrimaryKey != "" && collection.PrimaryKey == collection.SortKey {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort_key cannot be the same as primary_key"})
		return
	}

	// Validate destinations
	for i, dest := range collection.Destinations {
		if dest.Endpoint == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("destination[%d]: endpoint is required", i)})
			return
		}
		if len(dest.EventTypes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("destination[%d]: at least one event type is required", i)})
			return
		}
		if err := dest.ValidateEventTypes(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("destination[%d]: %v", i, err)})
			return
		}
	}

	// Deletion protection is mandatory on create
	collection.DeletionProtection = true

	if err := s.collectionStore.Create(ctx, &collection); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Apply TTL index if configured
	if collection.TTLAttribute != "" {
		if err := s.mongoClient.CreateTTLIndex(ctx, collectionName, collection.TTLAttribute); err != nil {
			log.Printf("Failed to create TTL index for %s: %v", collectionName, err)
		}
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, collectionName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusCreated, collection)
}

func (s *Server) updateCollection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var collection collections.Collection
	if err := c.ShouldBindJSON(&collection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	collectionName := collection.CollectionName
	if collectionName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Collection name is required"})
		return
	}
	collection.CollectionName = collectionName

	// Collection name in path must match body
	if collectionName != name {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Collection name in path must match body"})
		return
	}
	if collection.SortKey != "" && collection.PrimaryKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "primary_key is required when sort_key is defined"})
		return
	}
	if collection.PrimaryKey != "" && collection.PrimaryKey == collection.SortKey {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sort_key cannot be the same as primary_key"})
		return
	}

	// Validate destinations
	for i, dest := range collection.Destinations {
		if dest.Endpoint == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("destination[%d]: endpoint is required", i)})
			return
		}
		if len(dest.EventTypes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("destination[%d]: at least one event type is required", i)})
			return
		}
		if err := dest.ValidateEventTypes(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("destination[%d]: %v", i, err)})
			return
		}
	}

	if err := s.collectionStore.Update(ctx, &collection); err != nil {
		if err.Error() == "collection not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Apply TTL index if configured (create or update)
	if collection.TTLAttribute != "" {
		if err := s.mongoClient.CreateTTLIndex(ctx, collectionName, collection.TTLAttribute); err != nil {
			log.Printf("Failed to create/update TTL index for %s: %v", collectionName, err)
		}
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, collectionName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusOK, collection)
}

func (s *Server) deleteCollection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	// Get collection to check deletion protection and get collection name for notification
	collection, err := s.collectionStore.Get(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}

	// Check deletion protection
	if collection.DeletionProtection {
		c.JSON(http.StatusForbidden, gin.H{"error": "Deletion protection is enabled. Disable it before deleting the collection."})
		return
	}

	if err := s.collectionStore.Delete(ctx, name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, collection.CollectionName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Document handlers (data plane CRUD)

func (s *Server) listDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	collectionConfig, err := s.collectionStore.Get(ctx, collectionName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}
	pk, sk := resolveKeyQueryValues(c, collectionConfig)
	id := c.Query("id")

	if sk != "" && pk == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk query param is required when sk is provided"})
		return
	}

	// DynamoDB-style single document lookup when key params are provided.
	if pk != "" || id != "" {
		store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

		var (
			doc  bson.M
			opErr error
		)

		if pk != "" {
			if collectionConfig.PrimaryKey == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "this collection has no primary_key configured; use id for MongoDB _id lookup"})
				return
			}
			if collectionConfig.SortKey != "" && sk == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "sk query param is required for this collection"})
				return
			}
			doc, opErr = store.GetByKeys(ctx, collectionConfig.PrimaryKey, collectionConfig.SortKey, pk, sk)
		} else {
			doc, opErr = store.Get(ctx, id)
		}

		if opErr != nil {
			if opErr.Error() == "document not found" || strings.Contains(opErr.Error(), "not found") {
				c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": opErr.Error()})
			return
		}

		c.JSON(http.StatusOK, doc)
		return
	}

	// Parse query params
	page := int64(1)
	limit := int64(20)

	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.ParseInt(p, 10, 64); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// Parse filter JSON
	var filter bson.M
	if f := c.Query("filter"); f != "" {
		if err := json.Unmarshal([]byte(f), &filter); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid filter JSON"})
			return
		}
	}

	// Parse sort JSON
	var sort bson.M
	if so := c.Query("sort"); so != "" {
		if err := json.Unmarshal([]byte(so), &sort); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sort JSON"})
			return
		}
	}

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

	result, err := store.List(ctx, collections.DocumentQuery{
		Page:   page,
		Limit:  limit,
		Filter: filter,
		Sort:   sort,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) createDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

	var data bson.M
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	collectionConfig, err := s.collectionStore.Get(ctx, collectionName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}

	if collectionConfig.PrimaryKey != "" {
		rawPK, hasPK := data[collectionConfig.PrimaryKey]
		pk, pkOK := rawPK.(string)
		if !hasPK || !pkOK || pk == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is required and must be a non-empty string", collectionConfig.PrimaryKey)})
			return
		}
		if collectionConfig.SortKey != "" {
			rawSK, hasSK := data[collectionConfig.SortKey]
			sk, skOK := rawSK.(string)
			if !hasSK || !skOK || sk == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is required and must be a non-empty string", collectionConfig.SortKey)})
				return
			}
		}
	}

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

	result, err := store.Create(ctx, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, result)
}

func (s *Server) updateDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Query("id")

	var data bson.M
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	collectionConfig, err := s.collectionStore.Get(ctx, collectionName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}
	pk, sk := resolveKeyQueryValues(c, collectionConfig)

	if sk != "" && pk == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk query param is required when sk is provided"})
		return
	}

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

	// Update by DynamoDB-style keys when provided.
	if pk != "" {
		if collectionConfig.PrimaryKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "this collection has no primary_key configured; use id/_id for MongoDB _id update"})
			return
		}
		if collectionConfig.SortKey != "" && sk == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sk query param is required for this collection"})
			return
		}
		result, opErr := store.UpdateByKeys(ctx, collectionConfig.PrimaryKey, collectionConfig.SortKey, pk, sk, data)
		if opErr != nil {
			if opErr.Error() == "document not found" || strings.Contains(opErr.Error(), "not found") {
				c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": opErr.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	// Legacy MongoDB _id update fallback.
	targetID := id
	if targetID == "" {
		if bodyID, ok := data["_id"].(string); ok && bodyID != "" {
			targetID = bodyID
		}
	}
	if targetID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk query param is required (sk optional), or use id/_id for MongoDB _id"})
		return
	}

	result, err := store.Update(ctx, targetID, data)
	if err != nil {
		if err.Error() == "document not found" || strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) deleteDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Query("id")

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)
	collectionConfig, err := s.collectionStore.Get(ctx, collectionName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}
	pk, sk := resolveKeyQueryValues(c, collectionConfig)

	if sk != "" && pk == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk query param is required when sk is provided"})
		return
	}

	var opErr error

	if pk != "" {
		if collectionConfig.PrimaryKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "this collection has no primary_key configured; use id for MongoDB _id delete"})
			return
		}
		if collectionConfig.SortKey != "" && sk == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sk query param is required for this collection"})
			return
		}
		opErr = store.DeleteByKeys(ctx, collectionConfig.PrimaryKey, collectionConfig.SortKey, pk, sk)
	} else if id != "" {
		opErr = store.Delete(ctx, id)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk query param is required (sk optional), or use id for MongoDB _id"})
		return
	}

	if opErr != nil {
		if opErr.Error() == "document not found" || strings.Contains(opErr.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": opErr.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// getRequiredEnv gets a required environment variable or exits
func getRequiredEnv(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	log.Fatalf("Required environment variable %s is not set", key)
	return ""
}

// getEnv gets an environment variable with a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func resolveKeyQueryValues(c *gin.Context, collection *collections.Collection) (string, string) {
	pk := c.Query("pk")
	sk := c.Query("sk")

	if pk == "" && collection != nil && collection.PrimaryKey != "" {
		pk = c.Query(collection.PrimaryKey)
	}
	if sk == "" && collection != nil && collection.SortKey != "" {
		sk = c.Query(collection.SortKey)
	}

	return pk, sk
}
