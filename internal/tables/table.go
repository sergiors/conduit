package tables

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Table represents a DynamoDB-style table configuration
type Table struct {
	ID            string    `bson:"_id,omitempty"`
	TableName     string    `bson:"table_name"`
	StreamEnabled bool      `bson:"stream_enabled"`
	OldImage      bool      `bson:"old_image"`
	TTLField      string    `bson:"ttl_field,omitempty"`
	Destinations  []string  `bson:"destinations"`
	CreatedAt     time.Time `bson:"created_at"`
	UpdatedAt     time.Time `bson:"updated_at"`
}

// Store manages table configurations in MongoDB
type Store struct {
	collection *mongo.Collection
}

// NewStore creates a new table store
func NewStore(client *mongo.Client, database string) *Store {
	return &Store{
		collection: client.Database(database).Collection("system.tables"),
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

// Create inserts a new table configuration
func (s *Store) Create(ctx context.Context, table *Table) error {
	now := time.Now()
	table.CreatedAt = now
	table.UpdatedAt = now

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

// Delete removes a table configuration
func (s *Store) Delete(ctx context.Context, name string) error {
	_, err := s.collection.DeleteOne(ctx, bson.M{"table_name": name})
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
