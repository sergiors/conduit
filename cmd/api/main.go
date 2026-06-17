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
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/swaggo/files"
	"github.com/swaggo/gin-swagger"
	"go.mongodb.org/mongo-driver/bson"

	_ "github.com/sergiors/conduit/docs"
)

type Server struct {
	collectionStore *collections.Store
	mongoClient     *mongo.Client
	redisClient     *redis.Client
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
		collectionStore: store,
		mongoClient:     client,
		redisClient:     redisClient,
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

	// Collection sinks
	router.GET("/api/collections/:name/sinks", server.getSinks)
	router.PUT("/api/collections/:name/sinks", server.updateSinks)

	// Collection documents CRUD (data plane)
	router.GET("/api/collections/:name/documents", server.listDocuments)
	router.POST("/api/collections/:name/documents", server.createDocument)
	router.GET("/api/collections/:name/documents/:id", server.getDocument)
	router.PUT("/api/collections/:name/documents/:id", server.updateDocument)
	router.DELETE("/api/collections/:name/documents/:id", server.deleteDocument)

	router.GET("/health", server.handleHealth)

	// Swagger
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	port := getEnv("PORT", "8080")
	log.Printf("API server starting on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// @Summary List all collections
// @Description List all collection configurations
// @Produce json
// @Success 200 {array} collections.Collection
// @Router /api/collections [get]
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

// @Summary Get collection
// @Description Get a collection configuration by name
// @Produce json
// @Param name path string true "Collection name"
// @Success 200 {object} collections.Collection
// @Failure 404 {object} map[string]string
// @Router /api/collections/{name} [get]
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

// @Summary Create collection
// @Description Creates a new collection configuration
// @Accept json
// @Produce json
// @Param collection body collections.Collection true "Collection data"
// @Success 201 {object} collections.Collection
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections [post]
func (s *Server) createCollection(c *gin.Context) {
	ctx := c.Request.Context()

	var collection collections.Collection
	if err := c.ShouldBindJSON(&collection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Deletion protection is mandatory on create
	collection.DeletionProtection = true

	if err := s.collectionStore.Create(ctx, &collection); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, collection.CollectionName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusCreated, collection)
}

// @Summary Update collection
// @Description Update a collection configuration
// @Accept json
// @Produce json
// @Param name path string true "Collection name"
// @Param collection body collections.Collection true "Collection data"
// @Success 200 {object} collections.Collection
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name} [put]
func (s *Server) updateCollection(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var collection collections.Collection
	if err := c.ShouldBindJSON(&collection); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	collection.CollectionName = name

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
		if err := s.mongoClient.CreateTTLIndex(ctx, name, collection.TTLAttribute); err != nil {
			log.Printf("Failed to create/update TTL index for %s: %v", name, err)
		}
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, name); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusOK, collection)
}

// @Summary Get collection sinks
// @Description Get sinks for a collection
// @Produce json
// @Param name path string true "Collection name"
// @Success 200 {array} collections.SinkConfig
// @Failure 404 {object} map[string]string
// @Router /api/collections/{name}/sinks [get]
func (s *Server) getSinks(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	sinks, err := s.collectionStore.GetSinks(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
		return
	}

	c.JSON(http.StatusOK, sinks)
}

// @Summary Update collection sinks
// @Description Replace sinks for a collection (stream_enabled must be true)
// @Accept json
// @Produce json
// @Param name path string true "Collection name"
// @Param sinks body []collections.SinkConfig true "Sinks data"
// @Success 200 {array} collections.SinkConfig
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /api/collections/{name}/sinks [put]
func (s *Server) updateSinks(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var sinks []collections.SinkConfig
	if err := c.ShouldBindJSON(&sinks); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Validate sinks
	for i, sink := range sinks {
		if sink.Endpoint == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("sink[%d]: endpoint is required", i)})
			return
		}
		if len(sink.EventTypes) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("sink[%d]: at least one event type is required", i)})
			return
		}
		if err := sink.ValidateEventTypes(); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("sink[%d]: %v", i, err)})
			return
		}
	}

	if err := s.collectionStore.UpdateSinks(ctx, name, sinks); err != nil {
		if err.Error() == "collection not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Collection not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, name); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusOK, sinks)
}

// @Summary Delete collection
// @Description Delete a collection configuration
// @Param name path string true "Collection name"
// @Success 204
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name} [delete]
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

// @Summary Health check
// @Description Check if the API is running
// @Produce json
// @Success 200 {object} map[string]string
// @Router /health [get]
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// @Summary List documents
// @Description List documents in a collection with pagination and filtering
// @Produce json
// @Param name path string true "Collection name"
// @Param page query int false "Page number" default(1)
// @Param limit query int false "Items per page" default(20)
// @Param filter query string false "Filter as JSON"
// @Param sort query string false "Sort as JSON"
// @Success 200 {object} interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name}/documents [get]
func (s *Server) listDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")

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

// @Summary Get document
// @Description Get a document by ID in a collection
// @Produce json
// @Param name path string true "Collection name"
// @Param id path string true "Document ID"
// @Success 200 {object} interface{}
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name}/documents/{id} [get]
func (s *Server) getDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

	doc, err := store.Get(ctx, id)
	if err != nil {
		if err.Error() == "document not found" || strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, doc)
}

// @Summary Create document
// @Description Create a new document in a collection
// @Accept json
// @Produce json
// @Param name path string true "Collection name"
// @Param document body interface{} true "Document data"
// @Success 201 {object} interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name}/documents [post]
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

	if collectionConfig.PartitionKey != "" {
		rawPK, hasPK := data[collectionConfig.PartitionKey]
		pk, pkOK := rawPK.(string)
		if !hasPK || !pkOK || pk == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is required and must be a non-empty string", collectionConfig.PartitionKey)})
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

// @Summary Update document
// @Description Update a document by ID in a collection
// @Accept json
// @Produce json
// @Param name path string true "Collection name"
// @Param id path string true "Document ID"
// @Param document body interface{} true "Document data"
// @Success 200 {object} interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name}/documents/{id} [put]
func (s *Server) updateDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	var data bson.M
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

	result, err := store.Update(ctx, id, data)
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

// @Summary Delete document
// @Description Delete a document by ID in a collection
// @Param name path string true "Collection name"
// @Param id path string true "Document ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/collections/{name}/documents/{id} [delete]
func (s *Server) deleteDocument(c *gin.Context) {
	ctx := c.Request.Context()
	collectionName := c.Param("name")
	id := c.Param("id")

	store := collections.NewDocument(s.mongoClient.Client, s.mongoClient.Database(), collectionName)

	opErr := store.Delete(ctx, id)
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
