package api

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
)

// Dependencies holds the business/infrastructure packages the API layer needs.
type Dependencies struct {
	CollectionStore *collections.Store
	MongoClient     *mongo.Client
	RedisClient     *redis.Client
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

// notifyConfigChange publishes a configuration change notification to workers.
// Errors are logged but never returned to the client.
func (s *Server) notifyConfigChange(ctx context.Context, name string) {
	if err := s.deps.RedisClient.PublishConfigChange(ctx, name); err != nil {
		log.Printf("Failed to publish config change: %v", err)
	}
}
