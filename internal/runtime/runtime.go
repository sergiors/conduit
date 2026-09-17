// Package runtime is the application composition root: it creates the shared
// infrastructure dependencies (MongoDB and Redis clients, the collections
// manager), wires the two runtime components (the HTTP API and the CDC
// worker), starts them concurrently, and coordinates their lifecycle —
// graceful shutdown on context cancellation and shutdown of the surviving
// component when the other one fails. Shared clients are closed only after
// every consumer has stopped.
//
// Business logic lives in internal/api, internal/worker, and the packages
// below them; this package only performs dependency wiring and lifecycle
// coordination. It contains no process concerns (logging setup, signals,
// exit code) — those are owned by cmd/main.go — and no command parsing, which
// is owned by internal/cli.
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"conduit/internal/api"
	"conduit/internal/apikey"
	"conduit/internal/collections"
	"conduit/internal/config"
	"conduit/internal/mongo"
	"conduit/internal/redis"
	"conduit/internal/worker"
)

// Run is the composition-root entrypoint for `conduit start`.
//
// Dependencies are created up front, sharing a single bounded startup window
// (MongoDB may still be electing a PRIMARY after a restart, and NewClient
// waits for readiness before returning):
//
//  1. a shared MongoDB client (injected into both the API and the worker, so
//     one connection pool serves the whole process);
//  2. a shared Redis client (the API only publishes config-change/purge
//     notifications on it; the worker is its main consumer);
//  3. the shared collections manager, whose mutation side effects (Pub/Sub
//     notification, CDC-state purge) are wired to the Redis client as method
//     values so the collections package stays decoupled from Redis.
//
// Ownership: whoever creates a client closes it. This package owns both
// clients and closes them (Redis first, then MongoDB) only after the API and
// worker have fully stopped. The components never close what they did not
// create.
//
// Lifecycle: the server and the worker run concurrently off a single derived
// context. If the process root context is cancelled (SIGINT/SIGTERM), both
// components shut down gracefully; if either fails to start or run, its error
// cancels the other's context so it shuts down gracefully too, and the first
// error is returned.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	startupCtx, cancelStartup := context.WithTimeout(ctx, 60*time.Second)
	defer cancelStartup()

	// Shared MongoDB client, created once and ready to serve change streams
	// before any component starts. Created first: both consumers are injected
	// below and closed by this owner in the deferred shutdown last.
	mongoClient, err := mongo.NewClient(startupCtx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	// Deferred in this order so LIFO runs the closes only after both components
	// have stopped, Redis closer before the MongoDB closer.
	defer func() {
		if err := mongoClient.Close(context.Background()); err != nil {
			logger.Error("Failed to close MongoDB", "component", "mongo", "error", err)
		}
	}()

	redisClient, err := redis.NewClient(startupCtx, redis.Config{
		URI:    cfg.RedisURI,
		Prefix: "cdc:",
	}, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			logger.Error("Failed to close Redis", "component", "redis", "error", err)
		}
	}()

	// Shared collections manager: configuration (and DLQ persistence) is read
	// by the worker and mutated through the API. Mutation side effects —
	// publish a config-change notification and purge CDC state on delete — are
	// wired to the shared Redis client as method values so the collections
	// package stays decoupled from Redis.
	collectionsManager := collections.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)
	if err := collectionsManager.CreateIndex(startupCtx); err != nil {
		return fmt.Errorf("failed to create collection index: %w", err)
	}
	collectionsManager.OnPublish = redisClient.PublishConfigChange
	collectionsManager.OnPurge = redisClient.DeleteCollectionState

	// API-key manager and its index are API-layer infrastructure; they are
	// wired here because the composition root is where the shared MongoDB
	// client is available.
	apiKeys := apikey.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)
	if err := apiKeys.CreateIndex(startupCtx); err != nil {
		return fmt.Errorf("failed to create api key index: %w", err)
	}

	apiServer := api.New(api.Dependencies{
		Collections: collectionsManager,
		MongoClient: mongoClient,
		APIKeys:     apiKeys,
	})

	wkr, err := worker.NewWorker(cfg, logger, mongoClient, redisClient, collectionsManager)
	if err != nil {
		return err
	}

	// runCtx is cancelled when the process root context is cancelled
	// (SIGINT/SIGTERM) or when the first component fails, and both components
	// shut down gracefully before it is awaited.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- apiServer.Start(runCtx, cfg, logger)
	}()
	go func() {
		defer wg.Done()
		errs <- runWorker(runCtx, cfg, wkr)
	}()

	// Propagate the first component failure immediately so the sibling starts
	// shutting down, but still wait for both (drain errs) before returning. On
	// clean shutdown both send nil.
	var firstErr error
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	wg.Wait()
	return firstErr
}

// runWorker performs the worker's lifecycle under the shared cancellation
// domain: start it, block until the context is cancelled, then shut it down
// gracefully bounded by the configured shutdown timeout. A start failure shuts
// the already-started components down (inside Worker.Start) and is propagated
// verbatim so the runtime can stop the API.
func runWorker(ctx context.Context, cfg config.Config, wkr *worker.Worker) error {
	if err := wkr.Start(ctx); err != nil {
		return fmt.Errorf("worker failed: %w", err)
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := wkr.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("worker shutdown failed: %w", err)
	}
	return nil
}
