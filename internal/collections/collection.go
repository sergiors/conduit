package collections

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Collection struct {
	ID                 string    `bson:"_id,omitempty" json:"_id,omitempty"`
	CollectionName     string    `bson:"collection_name,omitempty" json:"collection_name,omitempty"`
	PartitionKey       string    `bson:"partition_key,omitempty" json:"partition_key,omitempty"`
	SortKey            string    `bson:"sort_key,omitempty" json:"sort_key,omitempty"`
	StreamEnabled      bool      `bson:"stream_enabled" json:"stream_enabled"`
	OldImage           bool      `bson:"old_image" json:"old_image"`
	TTLAttribute       string    `bson:"ttl_attribute,omitempty" json:"ttl_attribute,omitempty"`
	DeletionProtection bool      `bson:"deletion_protection" json:"deletion_protection"`
	CreatedAt          time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time `bson:"updated_at" json:"updated_at"`
}

// Settings manages collection configurations in MongoDB
type Settings struct {
	client     *mongo.Client
	database   string
	collection *mongo.Collection
	sinks      *mongo.Collection
}

// NewSettings creates a new collection settings manager
func NewSettings(client *mongo.Client, database string) *Settings {
	return &Settings{
		client:     client,
		database:   database,
		collection: client.Database(database).Collection("config.collections"),
		sinks:      client.Database(database).Collection("config.sinks"),
	}
}

// CreateIndex creates the indexes required by the settings.
func (s *Settings) CreateIndex(ctx context.Context) error {
	collectionIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "collection_name", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := s.collection.Indexes().CreateOne(ctx, collectionIndex); err != nil {
		return err
	}

	sinkIndex := mongo.IndexModel{
		Keys: bson.D{{Key: "collection_id", Value: 1}},
	}
	_, err := s.sinks.Indexes().CreateOne(ctx, sinkIndex)
	return err
}

// createCollection ensures the physical MongoDB collection exists.
func (s *Settings) createCollection(ctx context.Context, collection *Collection) error {
	db := s.client.Database(s.database)
	collectionName := collection.CollectionName

	// Check if collection already exists
	collections, err := db.ListCollectionNames(ctx, bson.M{"name": collectionName})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}

	if len(collections) > 0 {
		// Collection already exists, just ensure the key index
		if err := s.ensureKeyIndex(ctx, collectionName, collection.PartitionKey, collection.SortKey); err != nil {
			return fmt.Errorf("ensure key index: %w", err)
		}
		return nil
	}

	// Create the collection
	err = db.CreateCollection(ctx, collectionName)
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

func (s *Settings) ensureKeyIndex(ctx context.Context, collectionName, primaryKey, sortKey string) error {
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
func (s *Settings) Create(ctx context.Context, collection *Collection) error {
	name := collection.CollectionName
	if name == "" {
		return NewValidationError("collection name is required")
	}

	if collection.SortKey != "" && collection.PartitionKey == "" {
		return NewValidationError("partition_key is required when sort_key is defined")
	}

	if collection.PartitionKey != "" && collection.PartitionKey == collection.SortKey {
		return NewValidationError("sort_key cannot be the same as primary_key")
	}

	// Deletion protection is mandatory on create.
	collection.DeletionProtection = true

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
func (s *Settings) Get(ctx context.Context, name string) (*Collection, error) {
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
func (s *Settings) List(ctx context.Context) ([]Collection, error) {
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

// Delete removes a collection configuration and its MongoDB collection by collection name.
// Returns ErrCollectionNotFound if the collection does not exist, or
// ErrDeletionProtectionEnabled if deletion protection is on.
func (s *Settings) Delete(ctx context.Context, name string) error {
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

	// Delete associated sinks
	if err := s.deleteSinksByCollectionID(ctx, collection.ID); err != nil {
		return err
	}

	// Delete the configuration
	_, err = s.collection.DeleteOne(ctx, bson.M{"collection_name": name})
	return err
}
