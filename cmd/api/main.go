package main

import (
	"context"
	"log"
	"time"

	"github.com/sergiors/conduit/internal/api"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/config"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Close(context.Background())

	collectionStore := collections.NewStore(mongoClient.Client, cfg.MongoDBDatabase)
	if err := collectionStore.CreateIndex(ctx); err != nil {
		log.Fatalf("Failed to create collection index: %v", err)
	}

	redisClient, err := redis.NewClient(ctx, redis.Config{
		URI:    cfg.RedisURI,
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

	log.Printf("API server starting on port %s", cfg.Port)
	if err := server.Router().Run(":" + cfg.Port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
