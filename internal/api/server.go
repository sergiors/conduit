package api

import (
	"context"
	"fmt"
	"log"
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
// wires the publish/purge hooks, and blocks serving HTTP via Router().Run.
//
// Config is passed in (not loaded here). There is deliberately no signal
// handling or graceful HTTP shutdown: the API server blocks in Router().Run and
// relies on process termination — an accepted limitation of this codebase
// (acceptable only behind a trusted network).
//
// The function returns an error instead of the original logger.Fatalf so the CLI
// can exit non-zero on failure; fatal-on-invalid-config still happens earlier in
// config.Load.
func Run(cfg config.Config, logger *log.Logger) error {
	// Use a generous timeout for startup: MongoDB may still be electing a PRIMARY
	// after a restart, and NewClient waits for it before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, logger)
	if err != nil {
		return fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	defer mongoClient.Close(context.Background())

	collectionsManager := collections.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)
	if err := collectionsManager.CreateIndex(ctx); err != nil {
		return fmt.Errorf("failed to create collection index: %w", err)
	}

	apiKeys := apikey.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)
	if err := apiKeys.CreateIndex(ctx); err != nil {
		return fmt.Errorf("failed to create api key index: %w", err)
	}

	redisClient, err := redis.NewClient(ctx, redis.Config{
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

	logger.Printf("api server starting on port %s", cfg.Port)
	if err := server.Router().Run(":" + cfg.Port); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}
