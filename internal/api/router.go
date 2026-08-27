package api

import (
	"github.com/gin-gonic/gin"
)

func (s *Server) registerRoutes(r *gin.Engine) {
	// Health
	r.GET("/health", s.health)

	// Collection configuration
	r.GET("/api/collections", s.listCollections)
	r.POST("/api/collections", s.createCollection)
	r.GET("/api/collections/:name", s.getCollection)
	r.DELETE("/api/collections/:name", s.deleteCollection)

	// Collection streams
	r.PUT("/api/collections/:name/stream", s.enableStream)
	r.DELETE("/api/collections/:name/stream", s.disableStream)

	// Collection TTL
	r.PUT("/api/collections/:name/ttl", s.enableTTL)
	r.DELETE("/api/collections/:name/ttl", s.disableTTL)

	// Collection deletion protection
	r.PUT("/api/collections/:name/protection", s.enableProtection)
	r.DELETE("/api/collections/:name/protection", s.disableProtection)

	// Collection sinks
	r.GET("/api/collections/:name/sinks", s.getSinks)
	r.POST("/api/collections/:name/sinks", s.createSink)
	r.DELETE("/api/collections/:name/sinks/:sinkId", s.deleteSink)

	// Collection documents
	r.GET("/api/collections/:name/documents", s.listDocuments)
	r.GET("/api/collections/:name/documents/:id", s.getDocument)
}
