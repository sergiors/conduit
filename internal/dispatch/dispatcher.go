package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/sergiors/relay/internal/streams"
)

// Destination defines the interface for event destinations
type Destination interface {
	Send(ctx context.Context, record streams.StreamRecord) error
	Close() error
	Name() string
}

// Dispatcher routes stream records to configured destinations
type Dispatcher struct {
	destinations map[string][]Destination
	mu           sync.RWMutex
}

// NewDispatcher creates a new event dispatcher
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		destinations: make(map[string][]Destination),
	}
}

// Register adds a destination for a table
func (d *Dispatcher) Register(table string, dest Destination) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.destinations[table] == nil {
		d.destinations[table] = make([]Destination, 0)
	}
	d.destinations[table] = append(d.destinations[table], dest)
}

// Dispatch sends a stream record to all configured destinations
func (d *Dispatcher) Dispatch(ctx context.Context, table string, record streams.StreamRecord) error {
	d.mu.RLock()
	dests, ok := d.destinations[table]
	d.mu.RUnlock()

	if !ok {
		// No destinations configured for this table
		return nil
	}

	var lastErr error
	for _, dest := range dests {
		if err := dest.Send(ctx, record); err != nil {
			lastErr = err
			log.Printf("dispatch to %s failed: %v", dest.Name(), err)
		}
	}

	return lastErr
}

// Close all destinations
func (d *Dispatcher) Close() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var lastErr error
	for _, dests := range d.destinations {
		for _, dest := range dests {
			if err := dest.Close(); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// HTTPDestination sends records to an HTTP endpoint via POST
type HTTPDestination struct {
	name        string
	client      *http.Client
	endpoint    string
	eventTypes  map[string]bool
	bearerToken string
}

// NewHTTPDestination creates an HTTP destination
func NewHTTPDestination(endpoint string, bearerToken string, eventTypes []string) (*HTTPDestination, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}

	// Build event type filter
	eventTypeFilter := make(map[string]bool)
	for _, et := range eventTypes {
		eventTypeFilter[et] = true
	}

	// Default to all types if none specified
	if len(eventTypeFilter) == 0 {
		eventTypeFilter["INSERT"] = true
		eventTypeFilter["MODIFY"] = true
		eventTypeFilter["DELETE"] = true
	}

	return &HTTPDestination{
		name:        "http:" + endpoint,
		client:      &http.Client{Timeout: 30 * time.Second},
		endpoint:    endpoint,
		bearerToken: bearerToken,
		eventTypes:  eventTypeFilter,
	}, nil
}

func (h *HTTPDestination) Name() string {
	return h.name
}

func (h *HTTPDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	// Filter by event type
	if !h.eventTypes[string(record.RecordType)] {
		log.Printf("Skipping event type %s for HTTP destination", record.RecordType)
		return nil
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add bearer token if configured
	if h.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.bearerToken)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	log.Printf("Sent event to HTTP endpoint %s (status: %d)", h.endpoint, resp.StatusCode)
	return nil
}

func (h *HTTPDestination) Close() error {
	return nil
}

// EventBridgeDestination sends records to AWS EventBridge
type EventBridgeDestination struct {
	name         string
	eventBusName string
	// TODO: Add EventBridge client when integration is configured
}

// NewEventBridgeDestination creates an EventBridge destination
func NewEventBridgeDestination(eventBusName string) (*EventBridgeDestination, error) {
	return &EventBridgeDestination{
		name:         "eventbridge:" + eventBusName,
		eventBusName: eventBusName,
	}, nil
}

func (e *EventBridgeDestination) Name() string {
	return e.name
}

func (e *EventBridgeDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	// TODO: Put events to EventBridge
	// Example structure:
	// source: "relay-mongodb"
	// detail-type: record.RecordType
	// detail: { tableName, newImage, oldImage, timestamp }

	log.Printf("Would send to EventBridge %s: %+v", e.eventBusName, record)
	return nil
}

func (e *EventBridgeDestination) Close() error {
	return nil
}
