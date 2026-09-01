package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/config"
	"github.com/sergiors/conduit/internal/dispatch"
	_ "github.com/sergiors/conduit/internal/dispatch/transports" // Register transport builders via init()
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/retry"
	"github.com/sergiors/conduit/internal/watcher"
)

type Worker struct {
	mongoClient        *mongo.Client
	redisClient        *redis.Client
	collectionsManager *collections.Manager
	dispatcher         *dispatch.Dispatcher
	watcherManager     *watcher.Manager
	retryProcessor     *retry.Processor

	shutdownOnce atomic.Bool
}

func NewWorker(cfg config.Config) (*Worker, error) {
	// Use a generous timeout for startup: MongoDB may still be electing a PRIMARY
	// after a restart, and NewClient waits for it before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize MongoDB client. NewClient waits until MongoDB is ready (a
	// writable PRIMARY is reachable) before returning, so this returns once
	// MongoDB is fully ready to serve change streams.
	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	})
	if err != nil {
		return nil, err
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

	// Initialize collection manager
	collectionsManager := collections.NewManager(mongoClient.Client, cfg.MongoDBDatabase)

	// Initialize dispatcher
	dispatcher := dispatch.NewDispatcher()

	// Initialize retry processor
	retryProcessor := retry.NewProcessor(
		redisClient,
		dispatcher,
		retry.DefaultConfig(),
	)

	// Initialize watcher manager
	watcherCfg := watcher.DefaultConfig()
	watcherCfg.ProcessedEventTTL = cfg.ProcessedEventTTL
	watcherManager := watcher.NewManager(
		mongoClient.Client,
		cfg.MongoDBDatabase,
		collectionsManager,
		redisClient,
		dispatcher,
		retryProcessor,
		watcherCfg,
	)

	return &Worker{
		mongoClient:        mongoClient,
		redisClient:        redisClient,
		collectionsManager: collectionsManager,
		dispatcher:         dispatcher,
		watcherManager:     watcherManager,
		retryProcessor:     retryProcessor,
	}, nil
}

// Shutdown gracefully stops the worker in dependency order:
//
//  1. watcher manager (cancels the run ctx, waits for its loops and every
//     watcher, closes pub/sub) — no new events flow while bookkeeping drains;
//  2. retry processor (waits for the current processQueue pass to finish);
//  3. dispatcher (closes all sinks/transports);
//  4. redis client;
//  5. mongo client.
//
// Individual errors are collected and logged; the combined error is returned.
// Shutdown is idempotent: calling it more than once is a no-op.
func (w *Worker) Shutdown(ctx context.Context) error {
	if !w.shutdownOnce.CompareAndSwap(false, true) {
		return nil
	}

	log.Println("Shutting down worker...")

	var errs []error

	// Watcher manager goes first so no new events are dispatched while
	// in-flight bookkeeping completes.
	if err := w.watcherManager.Stop(ctx); err != nil {
		log.Printf("Error stopping watcher manager: %v", err)
		errs = append(errs, err)
	}

	if err := w.retryProcessor.Stop(ctx); err != nil {
		log.Printf("Error stopping retry processor: %v", err)
		errs = append(errs, err)
	}

	if err := w.dispatcher.Close(); err != nil {
		log.Printf("Error closing dispatcher: %v", err)
		errs = append(errs, err)
	}

	if err := w.redisClient.Close(); err != nil {
		log.Printf("Error closing Redis: %v", err)
		errs = append(errs, err)
	}

	if err := w.mongoClient.Close(ctx); err != nil {
		log.Printf("Error closing MongoDB: %v", err)
		errs = append(errs, err)
	}

	log.Println("Worker stopped")
	return errors.Join(errs...)
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
	cfg := config.LoadWorker()

	worker, err := NewWorker(cfg)
	if err != nil {
		log.Fatalf("Failed to create worker: %v", err)
	}

	// SIGINT and SIGTERM both trigger a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := worker.Run(ctx); err != nil {
		log.Printf("Worker failed: %v", err)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if serr := worker.Shutdown(shutdownCtx); serr != nil {
			log.Printf("Error during shutdown after run failure: %v", serr)
		}
		log.Fatalf("Worker failed: %v", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
