package dispatch

import (
	"context"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// Sink registry pattern:
// Each sink type (http, eventbridge, meilisearch) registers itself
// via init() in its package. The application's main.go must import the
// sinks package with a blank import to trigger initialization:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/sinks"
//
// This ensures all init() functions run before main(), registering all
// sink builders so BuildSink() can find them.

// Sink defines the interface for event sinks.
type Sink interface {
	Send(ctx context.Context, record streams.StreamRecord) error
	Close() error
	Name() string
}

// SinkBuilder builds a sink from config.
// Returns nil if required fields are missing.
type SinkBuilder func(ctx context.Context, collectionName string, sink collections.SinkConfig) Sink

// builders holds registered sink builders by type
var builders = make(map[string]SinkBuilder)

// RegisterSink registers a builder function for a sink type.
// Must be called during init() of the sink package.
func RegisterSink(sinkType string, builder SinkBuilder) {
	if builder == nil {
		log.Printf("Attempted to register nil builder for type: %s", sinkType)
		return
	}
	if _, exists := builders[sinkType]; exists {
		log.Printf("Sink builder for type %s already registered, overwriting", sinkType)
	}
	builders[sinkType] = builder
}

// BuildSink creates a sink using the registered builder.
// Returns nil if the type is not registered or build fails.
func BuildSink(ctx context.Context, collectionName string, sink collections.SinkConfig) Sink {
	builder, exists := builders[sink.Type]
	if !exists {
		log.Printf("Unknown sink type: %s for collection %s", sink.Type, collectionName)
		return nil
	}
	return builder(ctx, collectionName, sink)
}

// RegisteredSinkTypes returns all registered sink types.
func RegisteredSinkTypes() []string {
	types := make([]string, 0, len(builders))
	for t := range builders {
		types = append(types, t)
	}
	return types
}
