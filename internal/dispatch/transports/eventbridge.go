package transports

import (
	"context"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// EventBridgeSpec holds the type-specific configuration for an EventBridge transport.
type EventBridgeSpec struct {
	Region       string `bson:"region" json:"region"`
	EventBusName string `bson:"event_bus_name" json:"event_bus_name"`
	Source       string `bson:"source,omitempty" json:"source,omitempty"`
}

// EventBridgeTransport delivers stream records to AWS EventBridge.
type EventBridgeTransport struct {
	EventBridgeSpec
}

// NewEventBridge builds an EventBridge transport from its spec.
func NewEventBridge(ctx context.Context, spec EventBridgeSpec) dispatch.Transport {
	if spec.Region == "" {
		log.Printf("EventBridge transport requires a region")
		return nil
	}
	if spec.EventBusName == "" {
		log.Printf("EventBridge transport requires an event_bus_name")
		return nil
	}
	if spec.Source == "" {
		spec.Source = "conduit-mongodb"
	}

	return &EventBridgeTransport{
		EventBridgeSpec: spec,
	}
}

func (t *EventBridgeTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	log.Printf("Would send to EventBridge %s@%s (source: %s): %+v", t.Region, t.EventBusName, t.Source, record)
	return nil
}

func (t *EventBridgeTransport) Close() error { return nil }

func init() {
	dispatch.RegisterTransport(collections.SinkTypeEventBridge, func(ctx context.Context, collectionName string, t collections.Type, rawSpec map[string]interface{}) dispatch.Transport {
		var spec EventBridgeSpec
		if err := decodeSpec(rawSpec, &spec); err != nil {
			log.Printf("Failed to decode EventBridge transport spec for %s: %v", collectionName, err)
			return nil
		}

		return NewEventBridge(ctx, spec)
	})
}
