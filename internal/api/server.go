package api

import (
	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/mongo"
)

// Dependencies holds the business/infrastructure packages the API layer needs.
type Dependencies struct {
	CollectionSettings *collections.Settings
	MongoClient        *mongo.Client
	APIKey             string
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
