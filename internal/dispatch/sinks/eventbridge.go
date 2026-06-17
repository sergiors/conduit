package sinks

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// init registers the EventBridge sink builder automatically when this package is imported.
// This is triggered by the blank import in cmd/worker/main.go:
//
//	import _ "github.com/sergiors/conduit/internal/dispatch/sinks"

// EventBridgeSink sends records to AWS EventBridge.
type EventBridgeSink struct {
	name         string
	region       string
	eventBusName string
	source       string
	detailType   string
	// TODO: Add AWS SDK EventBridge client when integration is configured
}

// NewEventBridgeSink creates an EventBridge sink.
//   - region: AWS region (e.g. "us-east-1")
//   - eventBusName: name of the EventBridge event bus
//   - source: optional source identifier (default: "conduit-mongodb")
//   - detailType: optional detail-type (default: record.RecordType)
func NewEventBridgeSink(region, eventBusName, source, detailType string) (*EventBridgeSink, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required for EventBridge sink")
	}
	if eventBusName == "" {
		return nil, fmt.Errorf("event_bus_name is required for EventBridge sink")
	}
	if source == "" {
		source = "conduit-mongodb"
	}

	return &EventBridgeSink{
		name:         "eventbridge:" + eventBusName + "@" + region,
		region:       region,
		eventBusName: eventBusName,
		source:       source,
		detailType:   detailType,
	}, nil
}

func (e *EventBridgeSink) Name() string {
	return e.name
}

func (e *EventBridgeSink) Send(ctx context.Context, record streams.StreamRecord) error {
	dt := e.detailType
	if dt == "" {
		dt = string(record.RecordType)
	}

	// TODO: Use AWS SDK to call PutEvents
	// Example structure:
	// {
	//   Entries: []types.PutEventsRequestEntry{
	//     {
	//       EventBusName: e.eventBusName,
	//       Source:       e.source,
	//       DetailType:   dt,
	//       Detail:       json detail with tableName, newImage, oldImage, timestamp,
	//     },
	//   },
	// }

	log.Printf("Would send to EventBridge %s@%s (source: %s, detail-type: %s): %+v",
		e.region, e.eventBusName, e.source, dt, record)
	return nil
}

func (e *EventBridgeSink) Close() error {
	return nil
}

func init() {
	dispatch.RegisterSink("eventbridge", func(ctx context.Context, collectionName string, sink collections.SinkConfig) dispatch.Sink {
		region := sink.Region
		if region == "" {
			log.Printf("EventBridge sink for %s missing required 'region'", collectionName)
			return nil
		}
		busName := sink.EventBusName
		if busName == "" {
			busName = sink.Endpoint
		}
		if busName == "" {
			log.Printf("EventBridge sink for %s missing required 'event_bus_name' or 'endpoint'", collectionName)
			return nil
		}
		ebSink, err := NewEventBridgeSink(region, busName, sink.Source, "")
		if err != nil {
			log.Printf("Failed to create EventBridge sink for %s: %v", collectionName, err)
			return nil
		}
		return ebSink
	})
}
