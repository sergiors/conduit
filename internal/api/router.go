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
	r.POST("/api/collections/:name/stream", s.createStream)
	r.DELETE("/api/collections/:name/stream", s.deleteStream)

	// Collection TTL
	r.POST("/api/collections/:name/ttl", s.createTTL)
	r.DELETE("/api/collections/:name/ttl", s.deleteTTL)

	// Collection deletion protection
	r.POST("/api/collections/:name/protection", s.createProtection)
	r.DELETE("/api/collections/:name/protection", s.deleteProtection)

	// Collection sinks
	r.GET("/api/collections/:name/sinks", s.getSinks)
	r.POST("/api/collections/:name/sinks", s.createSink)
	r.DELETE("/api/collections/:name/sinks/:id", s.deleteSink)

	// Collection documents
	r.GET("/api/collections/:name/documents", s.listDocuments)
	r.GET("/api/collections/:name/documents/:id", s.getDocument)
}
