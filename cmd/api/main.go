package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/relay/internal/mongo"
	"github.com/sergiors/relay/internal/redis"
	"github.com/sergiors/relay/internal/tables"
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

	router.GET("/api/tables", server.listTables)
	router.POST("/api/tables", server.createTable)
	router.PUT("/api/tables/:id", server.updateTable)
	router.DELETE("/api/tables/:id", server.deleteTable)
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

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, table.TableName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusCreated, table)
}

func (s *Server) updateTable(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	var table tables.Table
	if err := c.ShouldBindJSON(&table); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if table.TableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name is required"})
		return
	}

	// Get existing table to validate table name cannot be changed
	existingTable, err := s.tableStore.GetByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Table not found"})
		return
	}

	// Table name cannot be changed
	if table.TableName != existingTable.TableName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name cannot be changed"})
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

	if err := s.tableStore.Update(ctx, id, &table); err != nil {
		if err.Error() == "table not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Table not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Notify worker of config change
	if err := s.redisClient.PublishConfigChange(ctx, table.TableName); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}

	c.JSON(http.StatusOK, table)
}

func (s *Server) deleteTable(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	if err := s.tableStore.Delete(ctx, id); err != nil {
		// Check for deletion protection error
		if err.Error() == "table not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Table not found"})
			return
		}
		if err.Error() == "deletion protection is enabled" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Deletion protection is enabled. Disable it before deleting the table."})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
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
