package main

import (
	"context"
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

	router.GET("/tables", server.listTables)
	router.POST("/tables", server.createTable)
	router.PUT("/tables", server.updateTable)
	router.DELETE("/tables", server.deleteTable)
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

	var table tables.Table
	if err := c.ShouldBindJSON(&table); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	if table.TableName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name is required"})
		return
	}

	if err := s.tableStore.Update(ctx, &table); err != nil {
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
	name := c.Query("name")

	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Table name is required"})
		return
	}

	if err := s.tableStore.Delete(ctx, name); err != nil {
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
