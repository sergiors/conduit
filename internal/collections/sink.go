package collections

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ValidEventTypes are the allowed event types for sinks.
var ValidEventTypes = []string{"INSERT", "MODIFY", "REMOVE"}

// Sink is a persisted sink configuration stored in config.sinks.
// The CollectionID references the _id of the owning collection in
// config.collections and is not exposed to API clients.
type Sink struct {
	ID           string                 `bson:"_id,omitempty" json:"id,omitempty"`
	CollectionID string                 `bson:"collectionId" json:"-"`
	Type         Type                   `bson:"type" json:"type"`
	Spec         map[string]interface{} `bson:"spec" json:"spec"`
	EventTypes   []string               `bson:"eventTypes,omitempty" json:"eventTypes"`
	Filter       Filter                 `bson:"filter,omitempty" json:"filter,omitempty"`
	Fingerprint  string                 `bson:"fingerprint,omitempty" json:"fingerprint,omitempty"`
	CreatedAt    time.Time              `bson:"createdAt" json:"createdAt"`
	UpdatedAt    time.Time              `bson:"updatedAt" json:"updatedAt"`
}

// ValidateEventTypes validates that all event types are valid.
func (s *Sink) ValidateEventTypes() error {
	if len(s.EventTypes) == 0 {
		return nil
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

// Identity returns a stable identifier for the sink.
func (s Sink) Identity() string {
	return s.ID
}

// fingerprintPayload is the canonical representation of a sink's immutable
// functional identity: type + spec.
type fingerprintPayload struct {
	Type Type                   `json:"type"`
	Spec map[string]interface{} `json:"spec"`
}

// Fingerprint returns a deterministic hash of the sink's immutable functional
// identity (type + spec). Two sinks with the same fingerprint deliver to the
// same destination and cannot coexist in one collection; CreateSink rejects
// them.
func (s *Sink) ComputeFingerprint() (string, error) {
	payload := fingerprintPayload{
		Type: s.Type,
		Spec: s.Spec,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal sink fingerprint: %w", err)
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// GetSink returns a single sink by its ID, scoped to the collection identified
// by name so a sink cannot be read from a different collection.
func (m *Manager) GetSink(ctx context.Context, collectionName, sinkID string) (*Sink, error) {
	collection, err := m.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}

	objectID, err := primitive.ObjectIDFromHex(sinkID)
	if err != nil {
		return nil, ErrSinkNotFound
	}

	var sink Sink
	err = m.sinks.FindOne(ctx, bson.M{"_id": objectID, "collectionId": collection.ID}).Decode(&sink)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrSinkNotFound
		}
		return nil, fmt.Errorf("find sink: %w", err)
	}
	return &sink, nil
}

// GetSinks returns the sinks for a collection identified by name.
func (m *Manager) GetSinks(ctx context.Context, collectionName string) ([]Sink, error) {
	collection, err := m.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}

	cursor, err := m.sinks.Find(ctx, bson.M{"collectionId": collection.ID})
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
func (m *Manager) CreateSink(ctx context.Context, collectionName string, sink Sink) (*Sink, error) {
	collection, err := m.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}

	if !collection.StreamEnabled {
		return nil, NewValidationError("streamEnabled must be true to configure sinks")
	}

	if err := sink.Validate(); err != nil {
		return nil, err
	}

	fp, err := sink.ComputeFingerprint()
	if err != nil {
		return nil, err
	}

	// Reject an equivalent sink before inserting; the unique compound
	// index (collectionId, fingerprint) covers concurrent creations.
	existing := m.sinks.FindOne(ctx, bson.M{"collectionId": collection.ID, "fingerprint": fp})
	if err := existing.Err(); err == nil {
		return nil, ErrSinkAlreadyExists
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, fmt.Errorf("find duplicate sink: %w", err)
	}

	now := time.Now()
	sink.ID = ""
	sink.CollectionID = collection.ID
	sink.Fingerprint = fp
	sink.CreatedAt = now
	sink.UpdatedAt = now

	result, err := m.sinks.InsertOne(ctx, sink)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, ErrSinkAlreadyExists
		}
		return nil, fmt.Errorf("insert sink: %w", err)
	}
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		sink.ID = objectID.Hex()
	}
	m.notifyPublish(ctx, collectionName)
	return &sink, nil
}

// DeleteSink deletes a sink by its ID, scoped to the collection identified by
// name so a sink cannot be removed from a different collection.
// On success, fires OnPublish (best-effort).
func (m *Manager) DeleteSink(ctx context.Context, collectionName, sinkID string) error {
	collection, err := m.Get(ctx, collectionName)
	if err != nil {
		return err
	}

	objectID, err := primitive.ObjectIDFromHex(sinkID)
	if err != nil {
		return ErrSinkNotFound
	}

	result, err := m.sinks.DeleteOne(ctx, bson.M{"_id": objectID, "collectionId": collection.ID})
	if err != nil {
		return fmt.Errorf("delete sink: %w", err)
	}
	if result.DeletedCount == 0 {
		return ErrSinkNotFound
	}
	m.notifyPublish(ctx, collectionName)
	return nil
}

// deleteSinksByCollectionID removes all sinks for a collection.
func (m *Manager) deleteSinksByCollectionID(ctx context.Context, collectionID string) error {
	_, err := m.sinks.DeleteMany(ctx, bson.M{"collectionId": collectionID})
	if err != nil {
		return fmt.Errorf("delete sinks: %w", err)
	}
	return nil
}

// SinkUpdate carries the fields a client may PATCH on a sink. Type and Spec
// are included structurally so an attempted identity change is detectable and
// rejected with ErrSinkIdentityImmutable.
type SinkUpdate struct {
	Filter     *Filter                `json:"filter,omitempty"`
	EventTypes []string               `json:"eventTypes,omitempty"`
	Type       *Type                  `json:"type,omitempty"`
	Spec       map[string]interface{} `json:"spec,omitempty"`
}

func specsEqual(a, b map[string]interface{}) bool {
	return reflect.DeepEqual(a, b)
}

// UpdateSink updates the mutable fields of an existing sink (filter,
// eventTypes). type and spec are immutable — changing them returns
// ErrSinkIdentityImmutable (create a new sink instead).
func (m *Manager) UpdateSink(ctx context.Context, collectionName, sinkID string, update SinkUpdate) (*Sink, error) {
	current, err := m.GetSink(ctx, collectionName, sinkID)
	if err != nil {
		return nil, err
	}

	if update.Type != nil && *update.Type != current.Type {
		return nil, ErrSinkIdentityImmutable
	}
	if update.Spec != nil && !specsEqual(current.Spec, update.Spec) {
		return nil, ErrSinkIdentityImmutable
	}

	if update.Filter == nil && update.EventTypes == nil {
		return nil, NewValidationError("no mutable fields provided for sink update")
	}

	updated := *current
	if update.Filter != nil {
		updated.Filter = *update.Filter
	}
	if update.EventTypes != nil {
		updated.EventTypes = update.EventTypes
	}

	if err := updated.ValidateEventTypes(); err != nil {
		return nil, err
	}

	updated.UpdatedAt = time.Now()

	result, err := m.sinks.UpdateOne(
		ctx,
		bson.M{"_id": mustObjectID(sinkID), "collectionId": current.CollectionID},
		bson.M{"$set": bson.M{
			"filter":      updated.Filter,
			"eventTypes": updated.EventTypes,
			"updatedAt":  updated.UpdatedAt,
		}},
	)
	if err != nil {
		return nil, fmt.Errorf("update sink: %w", err)
	}
	if result.MatchedCount == 0 {
		return nil, ErrSinkNotFound
	}

	m.notifyPublish(ctx, collectionName)
	return &updated, nil
}

// mustObjectID converts a hex string to an ObjectID, or returns primitive.NilObjectID.
func mustObjectID(hex string) primitive.ObjectID {
	objectID, _ := primitive.ObjectIDFromHex(hex)
	return objectID
}
