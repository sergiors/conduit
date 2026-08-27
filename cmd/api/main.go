package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/sergiors/conduit/internal/api"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
)

func main() {
	uri := getRequiredEnv("MONGODB_URI")
	database := getRequiredEnv("MONGODB_DATABASE")
	redisURI := getRequiredEnv("REDIS_URI")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      uri,
		Database: database,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Close(context.Background())

	collectionStore := collections.NewStore(mongoClient.Client, database)
	if err := collectionStore.CreateIndex(ctx); err != nil {
		log.Fatalf("Failed to create collection index: %v", err)
	}

	redisClient, err := redis.NewClient(ctx, redis.Config{
		URI:    redisURI,
		Prefix: "cdc:",
	})
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisClient.Close()

	server := api.New(api.Dependencies{
		CollectionStore: collectionStore,
		MongoClient:     mongoClient,
		RedisClient:     redisClient,
	})

	port := getEnv("PORT", "8080")
	log.Printf("API server starting on port %s", port)
	if err := server.Router().Run(":" + port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func getRequiredEnv(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	log.Fatalf("Required environment variable %s is not set", key)
	return ""
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
