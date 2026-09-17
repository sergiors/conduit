package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"conduit/internal/apikey"
	"conduit/internal/collections"
	"conduit/internal/config"
	"conduit/internal/mongo"
	"conduit/internal/redis"

	"github.com/gin-gonic/gin"
)

// Dependencies holds the business/infrastructure packages the API layer needs.
type Dependencies struct {
	Collections *collections.Manager
	MongoClient *mongo.Client
	APIKeys     *apikey.Manager
}

// Server exposes HTTP endpoints. It contains no business rules; it only binds
// requests, invokes the underlying packages, and serializes responses.
type Server struct {
	deps Dependencies
}

// New creates an HTTP server from the provided dependencies.
func New(deps Dependencies) *Server {
	return &Server{deps: deps}
}

// Router builds and returns the configured Gin router.
func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	s.registerRoutes(r)

	return r
}

// Run bootstraps and starts the API server. It does exactly what the former
// cmd/api main() did: connects MongoDB and Redis, creates the collection index,
// wires the publish/purge hooks, and blocks serving HTTP until the caller's
// context (the process root context — cancellation/SIGTERM is owned by the
// executable boundary) is cancelled, then performs a graceful HTTP shutdown
// bounded by the configured shutdown timeout.
//
// Config is passed in (not loaded here). The function returns an error instead
// of a fatal exit so the CLI can exit non-zero on failure;
// fatal-on-invalid-config still happens earlier in config.Load.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	// Use a generous timeout for startup: MongoDB may still be electing a PRIMARY
	// after a restart, and NewClient waits for it before returning. startupCtx
	// only bounds the initialization phase; the serve/shutdown loop below waits
	// on ctx itself (the unbounded process root context).
	startupCtx, cancelStartup := context.WithTimeout(ctx, 60*time.Second)
	defer cancelStartup()

	mongoClient, err := mongo.NewClient(startupCtx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer mongoClient.Close(context.Background())

	collectionsManager := collections.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)
	if err := collectionsManager.CreateIndex(startupCtx); err != nil {
		return fmt.Errorf("failed to create collection index: %w", err)
	}

	apiKeys := apikey.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)
	if err := apiKeys.CreateIndex(startupCtx); err != nil {
		return fmt.Errorf("failed to create api key index: %w", err)
	}

	redisClient, err := redis.NewClient(startupCtx, redis.Config{
		URI:    cfg.RedisURI,
		Prefix: "cdc:",
	}, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to Redis: %w", err)
	}
	defer redisClient.Close()

	// Infrastructure side effects of collections.Manager mutations: publish a
	// config-change notification and purge CDC state after a successful delete.
	// Injected as method values so both the collections package and the API
	// layer stay decoupled from Redis.
	collectionsManager.OnPublish = redisClient.PublishConfigChange
	collectionsManager.OnPurge = redisClient.DeleteCollectionState

	server := New(Dependencies{
		Collections: collectionsManager,
		MongoClient: mongoClient,
		APIKeys:     apiKeys,
	})

	// Serve HTTP and wait for the process root context to be cancelled
	// (SIGINT/SIGTERM handled by cmd/main.go), then shut the HTTP server down
	// gracefully, giving in-flight requests the same bounded window the worker
	// uses (cfg.ShutdownTimeout).
	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: server.Router(),
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("server failed: %w", err)
			close(serveErr)
			return
		}
		close(serveErr)
	}()

	logger.Info("API server starting", "port", cfg.Port)

	select {
	case err := <-serveErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Error during HTTP shutdown", "error", err)
			return fmt.Errorf("http shutdown failed: %w", err)
		}
		if err := <-serveErr; err != nil {
			return err
		}
	}
	return nil
}
