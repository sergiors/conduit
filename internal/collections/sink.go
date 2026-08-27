package collections

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ValidEventTypes are the allowed event types for sinks.
var ValidEventTypes = []string{"INSERT", "MODIFY", "REMOVE"}

// ErrSinkNotFound is returned when a sink does not exist.
var ErrSinkNotFound = errors.New("sink not found")

// SinkConfig represents a sink configuration.
// Common fields are used by all sinks; type-specific fields are
// documented below and applied by the watcher manager when building each
// sink.
type SinkConfig struct {
	Type           string         `bson:"type" json:"type"`
	Endpoint       string         `bson:"endpoint,omitempty" json:"endpoint"`                   // HTTP URL or Meilisearch host or EventBridge event-bus name
	BearerToken    string         `bson:"bearer_token,omitempty" json:"bearer_token,omitempty"` // HTTP auth token or Meilisearch API key
	EventTypes     []string       `bson:"event_types,omitempty" json:"event_types"`
	FilterCriteria FilterCriteria `bson:"filter_criteria,omitempty" json:"filter_criteria,omitempty"`

	// EventBridge-specific
	Region       string `bson:"region,omitempty" json:"region,omitempty"`                 // AWS region, e.g. "us-east-1"
	EventBusName string `bson:"event_bus_name,omitempty" json:"event_bus_name,omitempty"` // EventBridge event bus name
	Source       string `bson:"source,omitempty" json:"source,omitempty"`                 // Event source (default: "conduit-mongodb")

	// Meilisearch-specific
	IndexName string `bson:"index_name,omitempty" json:"index_name,omitempty"` // Target index (default: collection_name)
}

// ValidateEventTypes validates that all event types are valid.
// An empty slice is treated as "all event types" and is valid.
func (d *SinkConfig) ValidateEventTypes() error {
	if len(d.EventTypes) == 0 {
		return nil // Empty means all types, which is valid
	}

	validSet := make(map[string]bool)
	for _, et := range ValidEventTypes {
		validSet[et] = true
	}

	for _, et := range d.EventTypes {
		if !validSet[et] {
			return NewValidationError("invalid event type '%s': must be one of INSERT, MODIFY, REMOVE", et)
		}
	}
	return nil
}

// Validate checks the common sink configuration required by every sink type.
func (d *SinkConfig) Validate() error {
	if d.Endpoint == "" {
		return NewValidationError("endpoint is required")
	}
	if err := d.ValidateEventTypes(); err != nil {
		return err
	}
	return nil
}

// Sink is a persisted sink configuration stored in config.sinks.
// The CollectionID references the _id of the owning collection in
// config.collections and is not exposed to API clients.
type Sink struct {
	ID           string    `bson:"_id,omitempty" json:"id,omitempty"`
	CollectionID string    `bson:"collection_id" json:"-"`
	SinkConfig   `bson:",inline"`
	CreatedAt    time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time `bson:"updated_at" json:"updated_at"`
}

// GetSinks returns the sinks for a collection identified by name.
func (s *Store) GetSinks(ctx context.Context, collectionName string) ([]Sink, error) {
	collection, err := s.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}

	cursor, err := s.sinks.Find(ctx, bson.M{"collection_id": collection.ID})
	if err != nil {
		return nil, fmt.Errorf("find sinks: %w", err)
	}
	defer cursor.Close(ctx)

	var sinks []Sink
	if err := cursor.All(ctx, &sinks); err != nil {
		return nil, fmt.Errorf("decode sinks: %w", err)
	}
	if sinks == nil {
		sinks = []Sink{}
	}
	return sinks, nil
}

// CreateSink creates a sink for a collection identified by name.
func (s *Store) CreateSink(ctx context.Context, collectionName string, config SinkConfig) (*Sink, error) {
	collection, err := s.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}

	if !collection.StreamEnabled {
		return nil, NewValidationError("stream_enabled must be true to configure sinks")
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	now := time.Now()
	sink := Sink{
		CollectionID: collection.ID,
		SinkConfig:   config,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	result, err := s.sinks.InsertOne(ctx, sink)
	if err != nil {
		return nil, fmt.Errorf("insert sink: %w", err)
	}
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		sink.ID = objectID.Hex()
	}
	return &sink, nil
}

// DeleteSink deletes a sink by its ID, scoped to the collection identified by
// name so a sink cannot be removed from a different collection.
func (s *Store) DeleteSink(ctx context.Context, collectionName, sinkID string) error {
	collection, err := s.Get(ctx, collectionName)
	if err != nil {
		return err
	}

	objectID, err := primitive.ObjectIDFromHex(sinkID)
	if err != nil {
		return ErrSinkNotFound
	}

	result, err := s.sinks.DeleteOne(ctx, bson.M{"_id": objectID, "collection_id": collection.ID})
	if err != nil {
		return fmt.Errorf("delete sink: %w", err)
	}
	if result.DeletedCount == 0 {
		return ErrSinkNotFound
	}
	return nil
}

// deleteSinksByCollectionID removes all sinks for a collection.
func (s *Store) deleteSinksByCollectionID(ctx context.Context, collectionID string) error {
	_, err := s.sinks.DeleteMany(ctx, bson.M{"collection_id": collectionID})
	if err != nil {
		return fmt.Errorf("delete sinks: %w", err)
	}
	return nil
}
