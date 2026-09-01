package api

import (
	"github.com/gin-gonic/gin"
)

func (s *Server) registerRoutes(r *gin.Engine) {
	// Health
	r.GET("/health", s.health)

	// All /api/* routes are protected by bearer-token auth.
	api := r.Group("/api", authMiddleware(s.deps.APIKey))

	// Collection configuration
	api.GET("/collections", s.listCollections)
	api.POST("/collections", s.createCollection)
	api.GET("/collections/:name", s.getCollection)
	api.DELETE("/collections/:name", s.deleteCollection)

	// Collection streams
	api.POST("/collections/:name/stream", s.createStream)
	api.DELETE("/collections/:name/stream", s.deleteStream)

	// Collection TTL
	api.POST("/collections/:name/ttl", s.createTTL)
	api.DELETE("/collections/:name/ttl", s.deleteTTL)

	// Collection deletion protection
	api.POST("/collections/:name/protection", s.createProtection)
	api.DELETE("/collections/:name/protection", s.deleteProtection)

	// Collection sinks
	api.GET("/collections/:name/sinks", s.getSinks)
	api.POST("/collections/:name/sinks", s.createSink)
	api.DELETE("/collections/:name/sinks/:id", s.deleteSink)
	api.PATCH("/collections/:name/sinks/:id", s.updateSink)

	// Collection documents
	api.GET("/collections/:name/documents", s.listDocuments)
	api.GET("/collections/:name/documents/:id", s.getDocument)

	// Collection dead-letter queue
	api.GET("/collections/:name/dlq", s.listDLQ)
	api.GET("/collections/:name/dlq/:id", s.getDLQ)
}
