package collections

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ValidEventTypes are the allowed event types for sinks
var ValidEventTypes = []string{"INSERT", "MODIFY", "REMOVE"}

// Sentinel domain errors returned by the Store. Callers MUST identify them
// with errors.Is (never by comparing err.Error()), so HTTP status mapping
// does not depend on fragile string matching.
var (
	ErrCollectionNotFound        = errors.New("collection not found")
	ErrDeletionProtectionEnabled = errors.New("deletion protection is enabled")
	ErrDocumentNotFound          = errors.New("document not found")
	ErrTTLAttributeImmutable     = errors.New("TTL attribute is immutable")
	ErrValidation                = errors.New("validation failed")
)

// ValidationError wraps a dynamic client-validation message while remaining
// identifiable via errors.Is(err, ErrValidation). Use NewValidationError so
// HTTP handlers can map all validation errors to 400 through internal/apierr
// without repeating messages or comparing strings.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string        { return e.Message }
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

// NewValidationError returns a validation error with a formatted message.
func NewValidationError(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

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

// ValidateEventTypes validates that all event types are valid
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
			return fmt.Errorf("invalid event type '%s': must be one of INSERT, MODIFY, REMOVE", et)
		}
	}
	return nil
}

type Collection struct {
	ID                 string       `bson:"_id,omitempty" json:"_id,omitempty"`
	CollectionName     string       `bson:"collection_name,omitempty" json:"collection_name,omitempty"`
	PartitionKey       string       `bson:"partition_key,omitempty" json:"partition_key,omitempty"`
	SortKey            string       `bson:"sort_key,omitempty" json:"sort_key,omitempty"`
	StreamEnabled      bool         `bson:"stream_enabled" json:"stream_enabled"`
	OldImage           bool         `bson:"old_image" json:"old_image"`
	TTLAttribute       string       `bson:"ttl_attribute,omitempty" json:"ttl_attribute,omitempty"`
	Sinks              []SinkConfig `bson:"sinks" json:"sinks"`
	DeletionProtection bool         `bson:"deletion_protection" json:"deletion_protection"`
	CreatedAt          time.Time    `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time    `bson:"updated_at" json:"updated_at"`
}

// Store manages collection configurations in MongoDB
type Store struct {
	client     *mongo.Client
	database   string
	collection *mongo.Collection
}

// NewStore creates a new collection store
func NewStore(client *mongo.Client, database string) *Store {
	return &Store{
		client:     client,
		database:   database,
		collection: client.Database(database).Collection("config.collections"),
	}
}

// CreateIndex creates the unique index on collection name
func (s *Store) CreateIndex(ctx context.Context) error {
	currentIndexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "collection_name", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	_, err := s.collection.Indexes().CreateOne(ctx, currentIndexModel)
	return err
}

// createCollection creates the actual MongoDB collection for CDC monitoring
func (s *Store) createCollection(ctx context.Context, collection *Collection) error {
	db := s.client.Database(s.database)
	collectionName := collection.CollectionName

	// Check if collection already exists
	collections, err := db.ListCollectionNames(ctx, bson.M{"name": collectionName})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}

	if len(collections) > 0 {
		// Collection exists, enable changeStreamPreAndPostImages
		err = db.RunCommand(ctx, bson.M{
			"collMod":                      collectionName,
			"changeStreamPreAndPostImages": bson.M{"enabled": true},
		}).Err()
		if err != nil {
			log.Printf("Warning: Failed to enable changeStreamPreAndPostImages for %s: %v", collectionName, err)
		}

		if err := s.ensureKeyIndex(ctx, collectionName, collection.PartitionKey, collection.SortKey); err != nil {
			return fmt.Errorf("ensure key index: %w", err)
		}
		return nil
	}

	// Create the collection with changeStreamPreAndPostImages enabled
	err = db.CreateCollection(ctx, collectionName, options.CreateCollection().SetChangeStreamPreAndPostImages(bson.M{"enabled": true}))
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// Insert a dummy document to ensure the collection is not empty
	// (empty collections can cause issues with change streams)
	_, err = db.Collection(collectionName).InsertOne(ctx, bson.M{
		"_id":          "init",
		"_placeholder": true,
		"_created_at":  time.Now(),
	})
	if err != nil {
		return fmt.Errorf("insert placeholder: %w", err)
	}

	// Remove the placeholder document
	_, err = db.Collection(collectionName).DeleteOne(ctx, bson.M{"_id": "init"})
	if err != nil {
		return fmt.Errorf("delete placeholder: %w", err)
	}

	if err := s.ensureKeyIndex(ctx, collectionName, collection.PartitionKey, collection.SortKey); err != nil {
		return fmt.Errorf("ensure key index: %w", err)
	}

	return nil
}

func (s *Store) ensureKeyIndex(ctx context.Context, collectionName, primaryKey, sortKey string) error {
	if primaryKey == "" {
		return nil
	}
	if sortKey != "" && sortKey == primaryKey {
		return fmt.Errorf("sort key cannot be the same as primary key")
	}

	keys := bson.D{{Key: primaryKey, Value: 1}}
	indexName := "primary_key_idx"
	if sortKey != "" {
		keys = append(keys, bson.E{Key: sortKey, Value: 1})
		indexName = "primary_sort_key_idx"
	}

	indexModel := mongo.IndexModel{
		Keys:    keys,
		Options: options.Index().SetName(indexName).SetUnique(true),
	}

	_, err := s.client.Database(s.database).Collection(collectionName).Indexes().CreateOne(ctx, indexModel)
	return err
}

// Create inserts a new collection configuration and creates the MongoDB collection
func (s *Store) Create(ctx context.Context, collection *Collection) error {
	name := collection.CollectionName
	if name == "" {
		return fmt.Errorf("collection name is required")
	}

	if collection.SortKey != "" && collection.PartitionKey == "" {
		return fmt.Errorf("partition_key is required when sort_key is defined")
	}

	if collection.PartitionKey != "" && collection.PartitionKey == collection.SortKey {
		return fmt.Errorf("sort_key cannot be the same as primary_key")
	}

	now := time.Now()
	collection.CreatedAt = now
	collection.UpdatedAt = now

	// Create the actual MongoDB collection for CDC monitoring
	if err := s.createCollection(ctx, collection); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	result, err := s.collection.InsertOne(ctx, collection)
	if err != nil {
		return err
	}
	// Set the ID on the collection so it can be returned to the client
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		collection.ID = objectID.Hex()
	}
	return nil
}

// Get retrieves a collection by name
func (s *Store) Get(ctx context.Context, name string) (*Collection, error) {
	var collection Collection
	err := s.collection.FindOne(ctx, bson.M{"collection_name": name}).Decode(&collection)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCollectionNotFound
		}
		return nil, err
	}

	return &collection, nil
}

// List returns all collection configurations
func (s *Store) List(ctx context.Context) ([]Collection, error) {
	cursor, err := s.collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var collections []Collection
	if err := cursor.All(ctx, &collections); err != nil {
		return nil, err
	}
	return collections, nil
}

// SetDeletionProtection enables or disables deletion protection for a collection.
// It is idempotent: setting a collection to its current protection state is a no-op.
// Returns ErrCollectionNotFound if the collection does not exist.
func (s *Store) SetDeletionProtection(ctx context.Context, name string, enabled bool) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}

	_, err := s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"deletion_protection": enabled,
			"updated_at":          time.Now(),
		}},
	)
	return err
}

// SetStream enables the CDC stream for a collection and configures old_image.
// oldImage controls whether change events include the pre-image. Idempotent.
// Returns ErrCollectionNotFound if the collection does not exist.
func (s *Store) SetStream(ctx context.Context, name string, oldImage bool) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	_, err := s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"stream_enabled": true,
			"old_image":      oldImage,
			"updated_at":     time.Now(),
		}},
	)
	return err
}

// DisableStream disables the CDC stream and old_image for a collection.
// Idempotent. Returns ErrCollectionNotFound if the collection does not exist.
func (s *Store) DisableStream(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	_, err := s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"stream_enabled": false,
			"old_image":      false,
			"updated_at":     time.Now(),
		}},
	)
	return err
}

// SetTTL sets the collection TTL attribute. The TTL index itself is created
// by the caller via mongo.Client.CreateTTLIndex; the Store only persists the
// configuration and validates it.
// The attribute is immutable: if a different attribute is already set,
// ErrTTLAttributeImmutable is returned (delete it first). Idempotent for the
// same attribute. An empty attribute returns ErrValidation.
func (s *Store) SetTTL(ctx context.Context, name, attribute string) error {
	if attribute == "" {
		return NewValidationError("ttl attribute is required")
	}

	collection, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if collection.TTLAttribute != "" && collection.TTLAttribute != attribute {
		return ErrTTLAttributeImmutable
	}

	_, err = s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"ttl_attribute": attribute,
			"updated_at":    time.Now(),
		}},
	)
	if err != nil {
		return fmt.Errorf("set ttl attribute: %w", err)
	}
	return nil
}

// DisableTTL clears the TTL attribute and returns the previously-set attribute
// ("" if none), so the caller can drop the TTL index via mongo.Client.DropTTLIndex.
// Idempotent. Returns ErrCollectionNotFound if the collection does not exist.
func (s *Store) DisableTTL(ctx context.Context, name string) (string, error) {
	collection, err := s.Get(ctx, name)
	if err != nil {
		return "", err
	}

	if collection.TTLAttribute == "" {
		return "", nil
	}

	_, err = s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"ttl_attribute": "",
			"updated_at":    time.Now(),
		}},
	)
	if err != nil {
		return "", err
	}

	return collection.TTLAttribute, nil
}

// Delete removes a collection configuration and its MongoDB collection by collection name.
// Returns ErrCollectionNotFound if the collection does not exist, or
// ErrDeletionProtectionEnabled if deletion protection is on.
func (s *Store) Delete(ctx context.Context, name string) error {
	// Get collection to check deletion protection
	collection, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	// Check deletion protection
	if collection.DeletionProtection {
		return ErrDeletionProtectionEnabled
	}

	// Drop the MongoDB collection
	db := s.client.Database(s.database)
	if err := db.Collection(collection.CollectionName).Drop(ctx); err != nil {
		return fmt.Errorf("drop collection: %w", err)
	}

	// Delete the configuration
	_, err = s.collection.DeleteOne(ctx, bson.M{"collection_name": name})
	return err
}

// ListStreamEnabled returns collections with streams enabled
func (s *Store) ListStreamEnabled(ctx context.Context) ([]Collection, error) {
	cursor, err := s.collection.Find(ctx, bson.M{"stream_enabled": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var collections []Collection
	if err := cursor.All(ctx, &collections); err != nil {
		return nil, err
	}
	return collections, nil
}

// GetSinks returns the sinks for a collection
func (s *Store) GetSinks(ctx context.Context, collectionName string) ([]SinkConfig, error) {
	collection, err := s.Get(ctx, collectionName)
	if err != nil {
		return nil, err
	}
	return collection.Sinks, nil
}

// UpdateSinks replaces the sinks for a collection
func (s *Store) UpdateSinks(ctx context.Context, collectionName string, sinks []SinkConfig) error {
	collection, err := s.Get(ctx, collectionName)
	if err != nil {
		return ErrCollectionNotFound
	}

	if !collection.StreamEnabled {
		return fmt.Errorf("stream_enabled must be true to configure sinks")
	}

	for i, sink := range sinks {
		if err := sink.ValidateEventTypes(); err != nil {
			return fmt.Errorf("sink[%d]: %w", i, err)
		}
	}

	update := bson.M{
		"sinks":      sinks,
		"updated_at": time.Now(),
	}

	_, err = s.collection.UpdateOne(ctx, bson.M{"collection_name": collectionName}, bson.M{"$set": update})
	return err
}
