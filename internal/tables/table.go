package tables

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DestinationConfig represents a destination configuration
type DestinationConfig struct {
	Type        string   `bson:"type" json:"type"`
	Endpoint    string   `bson:"endpoint,omitempty" json:"endpoint"`
	BearerToken string   `bson:"bearer_token,omitempty" json:"bearer_token,omitempty"`
	EventTypes  []string `bson:"event_types,omitempty" json:"event_types"` // INSERT, MODIFY, DELETE
}

// Table represents a DynamoDB-style table configuration
type Table struct {
	ID                 string            `bson:"_id,omitempty" json:"_id,omitempty"`
	TableName          string            `bson:"table_name" json:"table_name"`
	StreamEnabled      bool              `bson:"stream_enabled" json:"stream_enabled"`
	OldImage           bool              `bson:"old_image" json:"old_image"`
	TTLField           string            `bson:"ttl_field,omitempty" json:"ttl_field,omitempty"`
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
		return nil // Collection already exists
	}

	// Create the collection
	if err := db.CreateCollection(ctx, tableName); err != nil {
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

	_, err := s.collection.InsertOne(ctx, table)
	return err
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

// Update modifies an existing table configuration
func (s *Store) Update(ctx context.Context, table *Table) error {
	table.UpdatedAt = time.Now()

	_, err := s.collection.UpdateOne(
		ctx,
		bson.M{"table_name": table.TableName},
		bson.M{"$set": table},
	)
	return err
}

// Delete removes a table configuration and its MongoDB collection
func (s *Store) Delete(ctx context.Context, name string) error {
	// Get table to check deletion protection
	table, err := s.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("get table: %w", err)
	}

	// Check deletion protection
	if table.DeletionProtection {
		return fmt.Errorf("deletion protection is enabled for table %s", name)
	}

	// Drop the MongoDB collection
	db := s.client.Database(s.database)
	if err := db.Collection(name).Drop(ctx); err != nil {
		return fmt.Errorf("drop collection: %w", err)
	}

	// Delete the configuration
	_, err = s.collection.DeleteOne(ctx, bson.M{"table_name": name})
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
