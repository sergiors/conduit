package collections

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ValidEventTypes are the allowed event types for destinations
var ValidEventTypes = []string{"INSERT", "MODIFY", "REMOVE"}

// DestinationConfig represents a destination configuration.
// Common fields are used by all destinations; type-specific fields are
// documented below and applied by the watcher manager when building each
// destination.
type DestinationConfig struct {
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
func (d *DestinationConfig) ValidateEventTypes() error {
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
	ID                   string              `bson:"_id,omitempty" json:"_id,omitempty"`
	CollectionName       string              `bson:"collection_name,omitempty" json:"collection_name,omitempty"`
	PrimaryKey           string              `bson:"primary_key,omitempty" json:"primary_key,omitempty"`
	SortKey              string              `bson:"sort_key,omitempty" json:"sort_key,omitempty"`
	StreamEnabled        bool                `bson:"stream_enabled" json:"stream_enabled"`
	OldImage             bool                `bson:"old_image" json:"old_image"`
	TTLAttribute         string              `bson:"ttl_attribute,omitempty" json:"ttl_attribute,omitempty"`
	Destinations         []DestinationConfig `bson:"destinations" json:"destinations"`
	DeletionProtection   bool                `bson:"deletion_protection" json:"deletion_protection"`
	CreatedAt            time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt            time.Time           `bson:"updated_at" json:"updated_at"`
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

		if err := s.ensureKeyIndex(ctx, collectionName, collection.PrimaryKey, collection.SortKey); err != nil {
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

	if err := s.ensureKeyIndex(ctx, collectionName, collection.PrimaryKey, collection.SortKey); err != nil {
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
	if collection.SortKey != "" && collection.PrimaryKey == "" {
		return fmt.Errorf("primary_key is required when sort_key is defined")
	}
	if collection.PrimaryKey != "" && collection.PrimaryKey == collection.SortKey {
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

// Update modifies an existing collection configuration by collection name
func (s *Store) Update(ctx context.Context, collection *Collection) error {
	name := collection.CollectionName

	// Get existing collection to validate changes
	existing, err := s.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("collection not found")
	}

	// collection name cannot be changed
	if name != existing.CollectionName {
		return fmt.Errorf("collection name cannot be changed: '%s' -> '%s'", existing.CollectionName, name)
	}

	// TTL attribute cannot be changed once set
	if existing.TTLAttribute != "" && collection.TTLAttribute != existing.TTLAttribute {
		return fmt.Errorf("TTL attribute cannot be changed once set: '%s' -> '%s'", existing.TTLAttribute, collection.TTLAttribute)
	}
	if existing.PrimaryKey != collection.PrimaryKey {
		return fmt.Errorf("primary_key cannot be changed once set")
	}
	if existing.SortKey != collection.SortKey {
		return fmt.Errorf("sort_key cannot be changed once set")
	}
	if collection.SortKey != "" && collection.PrimaryKey == "" {
		return fmt.Errorf("primary_key is required when sort_key is defined")
	}
	if collection.PrimaryKey != "" && collection.PrimaryKey == collection.SortKey {
		return fmt.Errorf("sort_key cannot be the same as primary_key")
	}

	update := bson.M{
		"collection_name":     collection.CollectionName,
		"primary_key":         collection.PrimaryKey,
		"sort_key":            collection.SortKey,
		"stream_enabled":      collection.StreamEnabled,
		"old_image":           collection.OldImage,
		"ttl_attribute":       collection.TTLAttribute,
		"destinations":        collection.Destinations,
		"deletion_protection": collection.DeletionProtection,
		"updated_at":          time.Now(),
	}

	_, err = s.collection.UpdateOne(ctx, bson.M{"collection_name": name}, bson.M{"$set": update})
	return err
}

// Delete removes a collection configuration and its MongoDB collection by collection name
func (s *Store) Delete(ctx context.Context, name string) error {
	// Get collection to check deletion protection
	collection, err := s.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("collection not found")
	}

	// Check deletion protection
	if collection.DeletionProtection {
		return fmt.Errorf("deletion protection is enabled")
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
