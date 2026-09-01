package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// Transport builders register themselves via init() in the transports package.
// main.go must import that package with a blank import so all init() functions
// run before main() and BuildTransport() can find them:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/transports"

// Transport defines the runtime interface responsible for delivering stream
// events to a destination. A transport knows only how to deliver an event;
// routing, filtering and identity belong to RuntimeSink.
type Transport interface {
	Send(ctx context.Context, record streams.StreamRecord) error
	Close() error
}

// TransportBuilder builds a ready-to-use Transport from a persisted spec,
// returning nil if the spec is invalid or unsupported at runtime. The collection
// name may feed transport-specific defaults (e.g. a Meilisearch index name);
// event types, filters and sink identity must not be handled here.
type TransportBuilder func(ctx context.Context, collectionName string, t collections.Type, spec map[string]interface{}) Transport

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
//
// Fail-closed: a configured sink must never be silently skipped. If the type is
// unregistered or the builder rejects the spec, BuildTransport returns a non-nil
// erroring transport. The sink still participates in dispatch and Send fails
// every matching event, so the watcher treats it as unsettled and retries; a nil
// return is never produced for a configured sink.
func BuildTransport(ctx context.Context, collectionName string, t collections.Type, spec map[string]interface{}) Transport {
	builder, exists := transportBuilders[t]
	if !exists {
		err := fmt.Errorf("no transport registered for sink type %q (collection %s)", t, collectionName)
		log.Print(err)
		return newUnavailableTransport(err)
	}
	transport := builder(ctx, collectionName, t, spec)
	if transport == nil {
		err := fmt.Errorf("transport builder rejected spec for sink type %q (collection %s)", t, collectionName)
		log.Print(err)
		return newUnavailableTransport(err)
	}
	return transport
}

// unavailableTransport fails Send for every matching event so a sink that
// cannot be built at runtime is never silently acknowledged; the watcher
// treats the error as unsettled and retries.
type unavailableTransport struct {
	err error
}

func newUnavailableTransport(err error) Transport {
	return &unavailableTransport{err: err}
}

func (t *unavailableTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	return fmt.Errorf("%w: %v", errUnavailable, t.err)
}

func (t *unavailableTransport) Close() error { return nil }

// errUnavailable is a sentinel so callers can identify an unavailable
// transport without string matching.
var errUnavailable = errors.New("sink transport unavailable")

// RegisteredTransportTypes returns all registered transport types.
func RegisteredTransportTypes() []collections.Type {
	types := make([]collections.Type, 0, len(transportBuilders))
	for t := range transportBuilders {
		types = append(types, t)
	}
	return types
}
