package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/relay-mongodb/internal/streams"
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

// RedisDestination sends records to Redis Streams
type RedisDestination struct {
	name   string
	client *redis.Client
	stream string
}

// NewRedisDestination creates a Redis destination using URI
func NewRedisDestination(redisURI string) (*RedisDestination, error) {
	opts, err := redis.ParseURL(redisURI)
	if err != nil {
		return nil, fmt.Errorf("parse redis URI: %w", err)
	}

	client := redis.NewClient(opts)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisDestination{
		name:   "redis:" + opts.Addr,
		client: client,
		stream: "cdc:events",
	}, nil
}

func (r *RedisDestination) Name() string {
	return r.name
}

func (r *RedisDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	// XADD to Redis Streams
	// Stream key: cdc:events:<table>
	// Format: * (auto-generated ID) with fields
	streamKey := fmt.Sprintf("cdc:events:%s", record.TableName)

	id, err := r.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		ID:     "*", // Auto-generate ID
		Values: map[string]interface{}{
			"data":      string(data),
			"timestamp": record.Timestamp.UnixNano(),
			"type":      string(record.RecordType),
		},
	}).Result()

	if err != nil {
		return fmt.Errorf("xadd to redis: %w", err)
	}

	log.Printf("Sent event to Redis Streams %s with ID %s", streamKey, id)
	return nil
}

func (r *RedisDestination) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
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
