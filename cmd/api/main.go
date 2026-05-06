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
	"go.mongodb.org/mongo-driver/bson"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/tables"
)

type Server struct {
	tableStore   *tables.Store
	mongoClient  *mongo.Client
	redisClient  *redis.Client
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

	store := tables.NewStore(client.Client, database)
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
		tableStore:  store,
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

	// Table configuration (control plane)
	router.GET("/api/tables", server.listTables)
	router.POST("/api/tables", server.createTable)
	router.GET("/api/tables/:name", server.getTable)
	router.PUT("/api/tables/:name", server.updateTable)
	router.DELETE("/api/tables/:name", server.deleteTable)

	// Table items CRUD (data plane)
	router.GET("/api/tables/:name/items", server.listItems)
	router.GET("/api/tables/:name/items", server.getItem)
	router.POST("/api/tables/:name/items", server.createItem)
	router.PUT("/api/tables/:name/items", server.updateItem)
	router.DELETE("/api/tables/:name/items", server.deleteItem)

	router.GET("/health", server.handleHealth)

	port := getEnv("PORT", "8080")
	log.Printf("API server starting on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func (s *Server) listTables(c *gin.Context) {
	ctx := c.Request.Context()
	tableList, err := s.tableStore.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if tableList == nil {
		tableList = []tables.Table{}
	}

	c.JSON(http.StatusOK, tableList)
}

func (s *Server) getTable(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	table, err := s.tableStore.Get(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Table not found"})
		return
	}

	c.JSON(http.StatusOK, table)
}

func (s *Server) createTable(c *gin.Context) {
	ctx := c.Request.Context()

	var table tables.Table
	if err := c.ShouldBindJSON(&table); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if table.TableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name is required"})
		return
	}

	// Validate destinations
	for i, dest := range table.Destinations {
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
	table.DeletionProtection = true

	if err := s.tableStore.Create(ctx, &table); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Apply TTL index if configured
	if table.TTLAttribute != "" {
		if err := s.mongoClient.CreateTTLIndex(ctx, table.TableName, table.TTLAttribute); err != nil {
			log.Printf("Failed to create TTL index for %s: %v", table.TableName, err)
		}
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, table.TableName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusCreated, table)
}

func (s *Server) updateTable(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	var table tables.Table
	if err := c.ShouldBindJSON(&table); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if table.TableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name is required"})
		return
	}

	// Table name in path must match body
	if table.TableName != name {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name in path must match body"})
		return
	}

	// Validate destinations
	for i, dest := range table.Destinations {
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

	if err := s.tableStore.Update(ctx, &table); err != nil {
		if err.Error() == "table not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Table not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Apply TTL index if configured (create or update)
	if table.TTLAttribute != "" {
		if err := s.mongoClient.CreateTTLIndex(ctx, table.TableName, table.TTLAttribute); err != nil {
			log.Printf("Failed to create/update TTL index for %s: %v", table.TableName, err)
		}
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, table.TableName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusOK, table)
}

func (s *Server) deleteTable(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")

	// Get table to check deletion protection and get table name for notification
	table, err := s.tableStore.Get(ctx, name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Table not found"})
		return
	}

	// Check deletion protection
	if table.DeletionProtection {
		c.JSON(http.StatusForbidden, gin.H{"error": "Deletion protection is enabled. Disable it before deleting the table."})
		return
	}

	if err := s.tableStore.Delete(ctx, name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, table.TableName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Item handlers (data plane CRUD)

func (s *Server) listItems(c *gin.Context) {
	ctx := c.Request.Context()
	tableName := c.Param("name")

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

	store := tables.NewItem(s.mongoClient.Client, s.mongoClient.Database(), tableName)

	result, err := store.List(ctx, tables.ItemQuery{
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

func (s *Server) getItem(c *gin.Context) {
	ctx := c.Request.Context()
	tableName := c.Param("name")

	// Get query params
	pk := c.Query("pk")
	sk := c.Query("sk")
	id := c.Query("id")

	store := tables.NewItem(s.mongoClient.Client, s.mongoClient.Database(), tableName)

	var item bson.M
	var err error

	if pk != "" {
		item, err = store.GetByKeys(ctx, pk, sk)
	} else if id != "" {
		item, err = store.Get(ctx, id)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk, sk, or id query param is required"})
		return
	}

	if err != nil {
		if err.Error() == "document not found" || strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, item)
}

func (s *Server) createItem(c *gin.Context) {
	ctx := c.Request.Context()
	tableName := c.Param("name")

	var data bson.M
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	store := tables.NewItem(s.mongoClient.Client, s.mongoClient.Database(), tableName)

	result, err := store.Create(ctx, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, result)
}

func (s *Server) updateItem(c *gin.Context) {
	ctx := c.Request.Context()
	tableName := c.Param("name")

	// Get query params for identifying the item
	pk := c.Query("pk")
	sk := c.Query("sk")
	id := c.Query("id")

	var data bson.M
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	store := tables.NewItem(s.mongoClient.Client, s.mongoClient.Database(), tableName)

	// Determine ID to update: pk/sk composite or _id
	var targetID string
	if pk != "" {
		// Build composite ID from pk/sk
		if sk != "" {
			targetID = pk + "#" + sk
		} else {
			targetID = pk
		}
		// Also allow body to override
		if bodyPK, ok := data["pk"].(string); ok && bodyPK != "" {
			targetID = bodyPK
			if bodySK, ok := data["sk"].(string); ok && bodySK != "" {
				targetID = bodyPK + "#" + bodySK
			}
		}
	} else if id != "" {
		targetID = id
	} else if bodyID, ok := data["_id"].(string); ok && bodyID != "" {
		targetID = bodyID
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk, sk, id query param or _id in body is required"})
		return
	}

	result, err := store.Update(ctx, targetID, data)
	if err != nil {
		if err.Error() == "document not found" || strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

func (s *Server) deleteItem(c *gin.Context) {
	ctx := c.Request.Context()
	tableName := c.Param("name")

	// Get query params
	pk := c.Query("pk")
	sk := c.Query("sk")
	id := c.Query("id")

	store := tables.NewItem(s.mongoClient.Client, s.mongoClient.Database(), tableName)

	var err error

	if pk != "" {
		err = store.DeleteByKeys(ctx, pk, sk)
	} else if id != "" {
		err = store.Delete(ctx, id)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pk, sk, or id query param is required"})
		return
	}

	if err != nil {
		if err.Error() == "document not found" || strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "Item not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
