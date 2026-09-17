package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"conduit/internal/apikey"
	"conduit/internal/collections"
	"conduit/internal/config"
	"conduit/internal/mongo"

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

// Start serves HTTP until the caller's context is cancelled (the process root
// context — cancellation/SIGTERM is owned by the executable boundary), then
// performs a graceful HTTP shutdown bounded by cfg.ShutdownTimeout (the same
// window the worker uses for its drain). An immediate serve failure (e.g. port
// conflict) is returned so the runtime can shut the other component down.
//
// MongoDB/Redis wiring happens in the composition root; Start only runs the
// HTTP surface. HTTP routes, middleware, and behavior are unchanged.
func (s *Server) Start(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	// Serve HTTP and wait for the process root context to be cancelled
	// (SIGINT/SIGTERM handled by cmd/main.go), then shut the HTTP server down
	// gracefully, giving in-flight requests the same bounded window the worker
	// uses (cfg.ShutdownTimeout).
	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: s.Router(),
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
