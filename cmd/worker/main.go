package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sergiors/relay/internal/dispatch"
	"github.com/sergiors/relay/internal/mongo"
	"github.com/sergiors/relay/internal/redis"
	"github.com/sergiors/relay/internal/retry"
	"github.com/sergiors/relay/internal/tables"
	"github.com/sergiors/relay/internal/watcher"
)

type Worker struct {
	mongoClient    *mongo.Client
	redisClient    *redis.Client
	tableStore     *tables.Store
	dispatcher     *dispatch.Dispatcher
	watcherManager *watcher.Manager
	retryProcessor *retry.Processor
}

func NewWorker() (*Worker, error) {
	// Get config from environment (REQUIRED - no defaults)
	mongoURI := getRequiredEnv("MONGODB_URI")
	mongoDatabase := getRequiredEnv("MONGODB_DATABASE")
	redisURI := getRequiredEnv("REDIS_URI") // Full Redis URI (DSN)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize MongoDB client
	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      mongoURI,
		Database: mongoDatabase,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	// Initialize replica set (required for change streams)
	if err := mongoClient.InitializeReplicaSet(ctx); err != nil {
		log.Printf("Warning: Failed to initialize replica set: %v", err)
	}

	// Initialize Redis client with URI/DSN
	redisClient, err := redis.NewClient(ctx, redis.Config{
		URI:      redisURI,
		Prefix:   "cdc:",
	})
	if err != nil {
		mongoClient.Close(ctx)
		return nil, err
	}

	// Initialize table store
	store := tables.NewStore(mongoClient.Client, mongoDatabase)

	// Initialize dispatcher
	dispatcher := dispatch.NewDispatcher()

	// Initialize watcher manager
	watcherManager := watcher.NewManager(
		mongoClient.Client,
		mongoDatabase,
		store,
		redisClient,
		dispatcher,
		watcher.Config{
			SyncInterval: 30 * time.Second,
			RedisURI:     redisURI,
		},
	)

	// Initialize retry processor
	retryProcessor := retry.NewProcessor(
		redisClient,
		dispatcher,
		retry.DefaultConfig(),
	)

	return &Worker{
		mongoClient:    mongoClient,
		redisClient:    redisClient,
		tableStore:     store,
		dispatcher:     dispatcher,
		watcherManager: watcherManager,
		retryProcessor: retryProcessor,
	}, nil
}

func (w *Worker) Close(ctx context.Context) error {
	log.Println("Closing worker...")

	// Stop watcher manager
	if err := w.watcherManager.Stop(ctx); err != nil {
		log.Printf("Error stopping watcher manager: %v", err)
	}

	// Close Redis
	if err := w.redisClient.Close(); err != nil {
		log.Printf("Error closing Redis: %v", err)
	}

	// Close MongoDB
	if err := w.mongoClient.Close(ctx); err != nil {
		log.Printf("Error closing MongoDB: %v", err)
	}

	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	log.Println("Worker starting...")

	// Start watcher manager
	if err := w.watcherManager.Start(ctx); err != nil {
		return err
	}

	// Start retry processor
	if err := w.retryProcessor.Start(ctx); err != nil {
		return err
	}

	log.Printf("Worker started with %d active watchers", w.watcherManager.GetActiveWatchers())

	return nil
}

func main() {
	worker, err := NewWorker()
	if err != nil {
		log.Fatalf("Failed to create worker: %v", err)
	}
	defer worker.Close(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := worker.Run(ctx); err != nil {
		log.Fatalf("Worker failed: %v", err)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down worker...")
	cancel()

	// Give time for graceful shutdown
	time.Sleep(2 * time.Second)
	log.Println("Worker stopped")
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
