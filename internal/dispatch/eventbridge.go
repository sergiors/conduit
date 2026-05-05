package dispatch

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/relay/internal/streams"
)

// EventBridgeDestination sends records to AWS EventBridge.
type EventBridgeDestination struct {
	name         string
	region       string
	eventBusName string
	source       string
	detailType   string
	// TODO: Add AWS SDK EventBridge client when integration is configured
}

// NewEventBridgeDestination creates an EventBridge destination.
//   - region: AWS region (e.g. "us-east-1")
//   - eventBusName: name of the EventBridge event bus
//   - source: optional source identifier (default: "relay-mongodb")
//   - detailType: optional detail-type (default: record.RecordType)
func NewEventBridgeDestination(region, eventBusName, source, detailType string) (*EventBridgeDestination, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required for EventBridge destination")
	}
	if eventBusName == "" {
		return nil, fmt.Errorf("event_bus_name is required for EventBridge destination")
	}
	if source == "" {
		source = "relay-mongodb"
	}

	return &EventBridgeDestination{
		name:         "eventbridge:" + eventBusName + "@" + region,
		region:       region,
		eventBusName: eventBusName,
		source:       source,
		detailType:   detailType,
	}, nil
}

func (e *EventBridgeDestination) Name() string {
	return e.name
}

func (e *EventBridgeDestination) Send(ctx context.Context, record streams.StreamRecord) error {
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

func (e *EventBridgeDestination) Close() error {
	return nil
}
