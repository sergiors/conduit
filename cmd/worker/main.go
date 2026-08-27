package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/config"
	"github.com/sergiors/conduit/internal/dispatch"
	_ "github.com/sergiors/conduit/internal/dispatch/sinks" // Register sink builders via init()
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/retry"
	"github.com/sergiors/conduit/internal/watcher"
)

type Worker struct {
	mongoClient     *mongo.Client
	redisClient     *redis.Client
	collectionStore *collections.Store
	dispatcher      *dispatch.Dispatcher
	watcherManager  *watcher.Manager
	retryProcessor  *retry.Processor
}

func NewWorker(cfg config.Config) (*Worker, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initialize MongoDB client
	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
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
		URI:    cfg.RedisURI,
		Prefix: "cdc:",
	})
	if err != nil {
		mongoClient.Close(ctx)
		return nil, err
	}

	// Initialize collection store
	store := collections.NewStore(mongoClient.Client, cfg.MongoDBDatabase)

	// Initialize dispatcher
	dispatcher := dispatch.NewDispatcher()

	// Initialize retry processor
	retryProcessor := retry.NewProcessor(
		redisClient,
		dispatcher,
		retry.DefaultConfig(),
	)

	// Initialize watcher manager
	watcherManager := watcher.NewManager(
		mongoClient.Client,
		cfg.MongoDBDatabase,
		store,
		redisClient,
		dispatcher,
		retryProcessor,
		watcher.Config{
			SyncInterval: 15 * time.Minute,
		},
	)

	return &Worker{
		mongoClient:     mongoClient,
		redisClient:     redisClient,
		collectionStore: store,
		dispatcher:      dispatcher,
		watcherManager:  watcherManager,
		retryProcessor:  retryProcessor,
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
	cfg := config.Load()

	worker, err := NewWorker(cfg)
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
