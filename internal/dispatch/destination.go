package dispatch

import (
	"context"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// Destination registry pattern:
// Each destination type (http, eventbridge, meilisearch) registers itself
// via init() in its package. The application's main.go must import the
// destinations package with a blank import to trigger initialization:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/destinations"
//
// This ensures all init() functions run before main(), registering all
// destination builders so BuildDestination() can find them.

// Destination defines the interface for event destinations.
type Destination interface {
	Send(ctx context.Context, record streams.StreamRecord) error
	Close() error
	Name() string
}

// DestinationBuilder builds a destination from config.
// Returns nil if required fields are missing.
type DestinationBuilder func(ctx context.Context, collectionName string, dest collections.DestinationConfig) Destination

// builders holds registered destination builders by type
var builders = make(map[string]DestinationBuilder)

// RegisterDestination registers a builder function for a destination type.
// Must be called during init() of the destination package.
func RegisterDestination(destType string, builder DestinationBuilder) {
	if builder == nil {
		log.Printf("Attempted to register nil builder for type: %s", destType)
		return
	}
	if _, exists := builders[destType]; exists {
		log.Printf("Destination builder for type %s already registered, overwriting", destType)
	}
	builders[destType] = builder
}

// BuildDestination creates a destination using the registered builder.
// Returns nil if the type is not registered or build fails.
func BuildDestination(ctx context.Context, collectionName string, dest collections.DestinationConfig) Destination {
	builder, exists := builders[dest.Type]
	if !exists {
		log.Printf("Unknown destination type: %s for collection %s", dest.Type, collectionName)
		return nil
	}
	return builder(ctx, collectionName, dest)
}

// RegisteredDestinationTypes returns all registered destination types.
func RegisteredDestinationTypes() []string {
	types := make([]string, 0, len(builders))
	for t := range builders {
		types = append(types, t)
	}
	return types
}
