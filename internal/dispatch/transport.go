package dispatch

import (
	"context"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// Transport registry pattern:
// Each transport type registers itself via init() in the transports package.
// The application's main.go must import the transports package with a blank
// import to trigger initialization:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/transports"
//
// This ensures all init() functions run before main(), registering all
// transport builders so BuildTransport() can find them.

// Transport defines the runtime interface responsible for delivering stream
// events to a destination. A transport knows only how to deliver an event;
// routing, filtering and identity belong to RuntimeSink.
type Transport interface {
	Send(ctx context.Context, record streams.StreamRecord) error
	Close() error
}

// TransportBuilder builds a transport from its persisted spec.
// The builder is responsible for decoding the type-specific spec payload,
// validating it, and returning a ready-to-use Transport. The collection name
// may be used for transport-specific defaults (e.g. a default Meilisearch
// index name), but event types, filter criteria and sink identity must not be
// handled here. If the spec is invalid or unsupported, the builder should
// return nil.
type TransportBuilder func(ctx context.Context, collectionName string, t collections.Type, spec map[string]interface{}) Transport

// transportBuilders holds registered transport builders by type.
var transportBuilders = make(map[collections.Type]TransportBuilder)

// RegisterTransport registers a builder function for a transport type.
// Must be called during init() of the transport package.
func RegisterTransport(t collections.Type, builder TransportBuilder) {
	if builder == nil {
		log.Printf("Attempted to register nil builder for type: %s", t)
		return
	}
	if _, exists := transportBuilders[t]; exists {
		log.Printf("Transport builder for type %s already registered, overwriting", t)
	}
	transportBuilders[t] = builder
}

// BuildTransport creates a transport using the registered builder.
// Returns nil if the type is not registered or the builder rejects the spec.
func BuildTransport(ctx context.Context, collectionName string, t collections.Type, spec map[string]interface{}) Transport {
	builder, exists := transportBuilders[t]
	if !exists {
		log.Printf("no transport registered for sink type %q (collection %s)", t, collectionName)
		return nil
	}
	return builder(ctx, collectionName, t, spec)
}

// RegisteredTransportTypes returns all registered transport types.
func RegisteredTransportTypes() []collections.Type {
	types := make([]collections.Type, 0, len(transportBuilders))
	for t := range transportBuilders {
		types = append(types, t)
	}
	return types
}
