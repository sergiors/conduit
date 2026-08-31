package collections

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ValidEventTypes are the allowed event types for sinks.
var ValidEventTypes = []string{"INSERT", "MODIFY", "REMOVE"}

// Sink is a persisted sink configuration stored in config.sinks.
// The CollectionID references the _id of the owning collection in
// config.collections and is not exposed to API clients.
type Sink struct {
	ID             string                 `bson:"_id,omitempty" json:"id,omitempty"`
	CollectionID   string                 `bson:"collection_id" json:"-"`
	Type           Type                   `bson:"type" json:"type"`
	Spec           map[string]interface{} `bson:"spec" json:"spec"`
	EventTypes     []string               `bson:"event_types,omitempty" json:"event_types"`
	FilterCriteria FilterCriteria         `bson:"filter_criteria,omitempty" json:"filter_criteria,omitempty"`
	CreatedAt      time.Time              `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time              `bson:"updated_at" json:"updated_at"`
}

// ValidateEventTypes validates that all event types are valid.
// An empty slice is treated as "all event types" and is valid.
func (s *Sink) ValidateEventTypes() error {
	if len(s.EventTypes) == 0 {
		return nil // Empty means all types, which is valid
	}

	validSet := make(map[string]bool)
	for _, et := range ValidEventTypes {
		validSet[et] = true
	}

	for _, et := range s.EventTypes {
		if !validSet[et] {
			return NewValidationError("invalid event type '%s': must be one of INSERT, MODIFY, REMOVE", et)
		}
	}
	return nil
}

// Validate checks the common sink configuration required by every sink type.
func (s *Sink) Validate() error {
	if s.Type == "" {
		return NewValidationError("sink type is required")
	}
	if len(s.Spec) == 0 {
		return NewValidationError("spec is required")
	}
	if err := s.ValidateEventTypes(); err != nil {
		return err
	}
	return nil
}

// Identity returns a stable identifier for the sink. Since sinks are immutable,
// the persisted ID is the only identity needed by the runtime.
func (s Sink) Identity() string {
	return s.ID
}

// GetSinks returns the sinks for a collection identified by name.
func (s *Settings) GetSinks(ctx context.Context, collectionName string) ([]Sink, error) {
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
// On success, fires OnPublish (best-effort).
func (s *Settings) CreateSink(ctx context.Context, collectionName string, sink Sink) (*Sink, error) {
	collection, err := s.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}

	if !collection.StreamEnabled {
		return nil, NewValidationError("stream_enabled must be true to configure sinks")
	}

	if err := sink.Validate(); err != nil {
		return nil, err
	}

	now := time.Now()
	sink.ID = ""
	sink.CollectionID = collection.ID
	sink.CreatedAt = now
	sink.UpdatedAt = now

	result, err := s.sinks.InsertOne(ctx, sink)
	if err != nil {
		return nil, fmt.Errorf("insert sink: %w", err)
	}
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		sink.ID = objectID.Hex()
	}
	s.notifyPublish(ctx, collectionName)
	return &sink, nil
}

// DeleteSink deletes a sink by its ID, scoped to the collection identified by
// name so a sink cannot be removed from a different collection.
// On success, fires OnPublish (best-effort).
func (s *Settings) DeleteSink(ctx context.Context, collectionName, sinkID string) error {
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
	s.notifyPublish(ctx, collectionName)
	return nil
}

// deleteSinksByCollectionID removes all sinks for a collection.
func (s *Settings) deleteSinksByCollectionID(ctx context.Context, collectionID string) error {
	_, err := s.sinks.DeleteMany(ctx, bson.M{"collection_id": collectionID})
	if err != nil {
		return fmt.Errorf("delete sinks: %w", err)
	}
	return nil
}
