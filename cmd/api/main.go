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

	// Use a generous timeout for startup: MongoDB may still be electing a PRIMARY
	// after a restart, and NewClient waits for it before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	})
	if err != nil {
		log.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Close(context.Background())

	collectionsManager := collections.NewManager(mongoClient.Client, cfg.MongoDBDatabase)
	if err := collectionsManager.CreateIndex(ctx); err != nil {
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

	// Infrastructure side effects of collections.Manager mutations: publish a
	// config-change notification and purge CDC state after a successful delete.
	// Injected as method values so both the collections package and the API
	// layer stay decoupled from Redis.
	collectionsManager.OnPublish = redisClient.PublishConfigChange
	collectionsManager.OnPurge = redisClient.DeleteCollectionState

	server := api.New(api.Dependencies{
		Collections: collectionsManager,
		MongoClient: mongoClient,
		APIKey:      cfg.APIKey,
	})

	log.Printf("API server starting on port %s", cfg.Port)
	if err := server.Router().Run(":" + cfg.Port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
