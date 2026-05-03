package tables

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

// DestinationConfig represents a destination configuration
type DestinationConfig struct {
	Type        string   `bson:"type" json:"type"`
	Endpoint    string   `bson:"endpoint,omitempty" json:"endpoint"`
	BearerToken string   `bson:"bearer_token,omitempty" json:"bearer_token,omitempty"`
	EventTypes  []string `bson:"event_types,omitempty" json:"event_types"` // INSERT, MODIFY, REMOVE
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

// Table represents a DynamoDB-style table configuration
type Table struct {
	ID                 string            `bson:"_id,omitempty" json:"_id,omitempty"`
	TableName          string            `bson:"table_name" json:"table_name"`
	StreamEnabled      bool              `bson:"stream_enabled" json:"stream_enabled"`
	OldImage           bool              `bson:"old_image" json:"old_image"`
	TTLAttribute       string            `bson:"ttl_attribute,omitempty" json:"ttl_attribute,omitempty"`
	Destinations       []DestinationConfig `bson:"destinations" json:"destinations"`
	DeletionProtection bool              `bson:"deletion_protection" json:"deletion_protection"`
	CreatedAt          time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time         `bson:"updated_at" json:"updated_at"`
}

// Store manages table configurations in MongoDB
type Store struct {
	client     *mongo.Client
	database   string
	collection *mongo.Collection
}

// NewStore creates a new table store
func NewStore(client *mongo.Client, database string) *Store {
	return &Store{
		client:     client,
		database:   database,
		collection: client.Database(database).Collection("config.tables"),
	}
}

// CreateIndex creates the unique index on table name
func (s *Store) CreateIndex(ctx context.Context) error {
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "table_name", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	_, err := s.collection.Indexes().CreateOne(ctx, indexModel)
	return err
}

// createCollection creates the actual MongoDB collection for CDC monitoring
func (s *Store) createCollection(ctx context.Context, tableName string) error {
	db := s.client.Database(s.database)

	// Check if collection already exists
	collections, err := db.ListCollectionNames(ctx, bson.M{"name": tableName})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}

	if len(collections) > 0 {
		// Collection exists, enable changeStreamPreAndPostImages
		err = db.RunCommand(ctx, bson.M{
			"collMod": tableName,
			"changeStreamPreAndPostImages": bson.M{"enabled": true},
		}).Err()
		if err != nil {
			log.Printf("Warning: Failed to enable changeStreamPreAndPostImages for %s: %v", tableName, err)
		}
		return nil
	}

	// Create the collection with changeStreamPreAndPostImages enabled
	err = db.CreateCollection(ctx, tableName, options.CreateCollection().SetChangeStreamPreAndPostImages(bson.M{"enabled": true}))
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// Insert a dummy document to ensure the collection is not empty
	// (empty collections can cause issues with change streams)
	_, err = db.Collection(tableName).InsertOne(ctx, bson.M{
		"_id":            "init",
		"_placeholder":   true,
		"_created_at":    time.Now(),
	})
	if err != nil {
		return fmt.Errorf("insert placeholder: %w", err)
	}

	// Remove the placeholder document
	_, err = db.Collection(tableName).DeleteOne(ctx, bson.M{"_id": "init"})
	if err != nil {
		return fmt.Errorf("delete placeholder: %w", err)
	}

	return nil
}

// Create inserts a new table configuration and creates the MongoDB collection
func (s *Store) Create(ctx context.Context, table *Table) error {
	now := time.Now()
	table.CreatedAt = now
	table.UpdatedAt = now

	// Create the actual MongoDB collection for CDC monitoring
	if err := s.createCollection(ctx, table.TableName); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	result, err := s.collection.InsertOne(ctx, table)
	if err != nil {
		return err
	}
	// Set the ID on the table so it can be returned to the client
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		table.ID = objectID.Hex()
	}
	return nil
}

// Get retrieves a table by name
func (s *Store) Get(ctx context.Context, name string) (*Table, error) {
	var table Table
	err := s.collection.FindOne(ctx, bson.M{"table_name": name}).Decode(&table)
	if err != nil {
		return nil, err
	}
	return &table, nil
}

// GetByID retrieves a table by its MongoDB ID
func (s *Store) GetByID(ctx context.Context, id string) (*Table, error) {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}
	var table Table
	err = s.collection.FindOne(ctx, bson.M{"_id": objectID}).Decode(&table)
	if err != nil {
		return nil, err
	}
	return &table, nil
}

// List returns all table configurations
func (s *Store) List(ctx context.Context) ([]Table, error) {
	cursor, err := s.collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var tables []Table
	if err := cursor.All(ctx, &tables); err != nil {
		return nil, err
	}
	return tables, nil
}

// Update modifies an existing table configuration by ID
func (s *Store) Update(ctx context.Context, id string, table *Table) error {
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}

	// Get existing table to validate table name cannot be changed
	existing, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("table not found")
	}

	// Table name cannot be changed
	if table.TableName != existing.TableName {
		return fmt.Errorf("table name cannot be changed")
	}

	// Create a copy to avoid modifying the original and immutable _id
	update := &Table{
		TableName:          table.TableName,
		StreamEnabled:      table.StreamEnabled,
		OldImage:           table.OldImage,
		TTLAttribute:       table.TTLAttribute,
		Destinations:       table.Destinations,
		DeletionProtection: table.DeletionProtection,
		UpdatedAt:          time.Now(),
	}

	result, err := s.collection.UpdateOne(
		ctx,
		bson.M{"_id": objectID},
		bson.M{"$set": update},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("table not found")
	}
	return err
}

// Delete removes a table configuration and its MongoDB collection by ID
func (s *Store) Delete(ctx context.Context, id string) error {
	// Get table to check deletion protection
	table, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("table not found")
	}

	// Check deletion protection
	if table.DeletionProtection {
		return fmt.Errorf("deletion protection is enabled")
	}

	// Drop the MongoDB collection
	db := s.client.Database(s.database)
	if err := db.Collection(table.TableName).Drop(ctx); err != nil {
		return fmt.Errorf("drop collection: %w", err)
	}

	// Delete the configuration
	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	_, err = s.collection.DeleteOne(ctx, bson.M{"_id": objectID})
	return err
}

// ListStreamEnabled returns tables with streams enabled
func (s *Store) ListStreamEnabled(ctx context.Context) ([]Table, error) {
	cursor, err := s.collection.Find(ctx, bson.M{"stream_enabled": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var tables []Table
	if err := cursor.All(ctx, &tables); err != nil {
		return nil, err
	}
	return tables, nil
}
