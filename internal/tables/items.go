package tables

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Item provides CRUD operations for items in a MongoDB collection
type Item struct {
	client     *mongo.Client
	database   string
	collection string
}

// NewItem creates a new item handler for a specific collection
func NewItem(client *mongo.Client, database, collection string) *Item {
	return &Item{
		client:     client,
		database:   database,
		collection: collection,
	}
}

// ItemQuery represents query parameters for scanning items
type ItemQuery struct {
	Page   int64  `json:"page"`
	Limit  int64  `json:"limit"`
	Filter bson.M `json:"filter"`
	Sort   bson.M `json:"sort"`
}

// ItemListResult represents the result of a scan operation
type ItemListResult struct {
	Items      []bson.M `json:"items"`
	Total      int64    `json:"total"`
	Page       int64    `json:"page"`
	Limit      int64    `json:"limit"`
	TotalPages int64    `json:"totalPages"`
}

// List returns items with pagination
func (i *Item) List(ctx context.Context, query ItemQuery) (*ItemListResult, error) {
	// Defaults
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	if query.Filter == nil {
		query.Filter = bson.M{}
	}
	if query.Sort == nil {
		query.Sort = bson.M{"_id": 1}
	}

	coll := i.client.Database(i.database).Collection(i.collection)

	// Count total
	total, err := coll.CountDocuments(ctx, query.Filter)
	if err != nil {
		return nil, fmt.Errorf("count documents: %w", err)
	}

	// Calculate pagination
	skip := (query.Page - 1) * query.Limit
	totalPages := (total + query.Limit - 1) / query.Limit
	if totalPages == 0 {
		totalPages = 1
	}

	// Find items
	opts := options.Find().
		SetSkip(skip).
		SetLimit(query.Limit).
		SetSort(query.Sort)

	cursor, err := coll.Find(ctx, query.Filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find documents: %w", err)
	}
	defer cursor.Close(ctx)

	var items []bson.M
	if err := cursor.All(ctx, &items); err != nil {
		return nil, fmt.Errorf("decode documents: %w", err)
	}

	if items == nil {
		items = []bson.M{}
	}

	return &ItemListResult{
		Items:      items,
		Total:      total,
		Page:       query.Page,
		Limit:      query.Limit,
		TotalPages: totalPages,
	}, nil
}

// Get retrieves an item by ID
func (i *Item) Get(ctx context.Context, id string) (bson.M, error) {
	coll := i.client.Database(i.database).Collection(i.collection)

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		// Try querying by string ID (pk/sk composite)
		result := coll.FindOne(ctx, bson.M{"_id": id})
		var item bson.M
		if err := result.Decode(&item); err != nil {
			if err == mongo.ErrNoDocuments {
				return nil, fmt.Errorf("document not found")
			}
			return nil, fmt.Errorf("find document: %w", err)
		}
		return item, nil
	}

	result := coll.FindOne(ctx, bson.M{"_id": objectID})
	var item bson.M
	if err := result.Decode(&item); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("document not found")
		}
		return nil, fmt.Errorf("find document: %w", err)
	}
	return item, nil
}

// GetByKeys retrieves an item by pk and sk
func (i *Item) GetByKeys(ctx context.Context, pk, sk string) (bson.M, error) {
	coll := i.client.Database(i.database).Collection(i.collection)

	query := bson.M{"pk": pk}
	if sk != "" {
		query["sk"] = sk
	}

	result := coll.FindOne(ctx, query)
	var item bson.M
	if err := result.Decode(&item); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("document not found")
		}
		return nil, fmt.Errorf("find document: %w", err)
	}
	return item, nil
}

// Create inserts a new item
func (i *Item) Create(ctx context.Context, data bson.M) (bson.M, error) {
	coll := i.client.Database(i.database).Collection(i.collection)

	// Set created_at if not present
	if _, ok := data["created_at"]; !ok {
		data["created_at"] = time.Now()
	}

	// Set updated_at if not present
	if _, ok := data["updated_at"]; !ok {
		data["updated_at"] = data["created_at"]
	}

	result, err := coll.InsertOne(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}

	// Add _id to returned document
	data["_id"] = result.InsertedID
	return data, nil
}

// Update updates an existing item by ID
func (i *Item) Update(ctx context.Context, id string, data bson.M) (bson.M, error) {
	coll := i.client.Database(i.database).Collection(i.collection)

	// Set updated_at
	data["updated_at"] = time.Now()

	// Don't allow updating _id
	delete(data, "_id")

	// Try to parse as ObjectID first, otherwise use string ID
	var idQuery interface{} = id
	if objectID, err := primitive.ObjectIDFromHex(id); err == nil {
		idQuery = objectID
	}

	result, err := coll.UpdateOne(
		ctx,
		bson.M{"_id": idQuery},
		bson.M{"$set": data},
		options.Update().SetUpsert(false),
	)
	if err != nil {
		return nil, fmt.Errorf("update document: %w", err)
	}

	if result.MatchedCount == 0 {
		return nil, fmt.Errorf("document not found")
	}

	// Return updated document with ID
	data["_id"] = id
	return data, nil
}

// Delete removes an item by ID
func (i *Item) Delete(ctx context.Context, id string) error {
	coll := i.client.Database(i.database).Collection(i.collection)

	// Try to parse as ObjectID first, otherwise use string ID
	var idQuery interface{} = id
	if objectID, err := primitive.ObjectIDFromHex(id); err == nil {
		idQuery = objectID
	}

	result, err := coll.DeleteOne(ctx, bson.M{"_id": idQuery})
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("document not found")
	}
	return nil
}

// DeleteByKeys removes an item by pk and sk
func (i *Item) DeleteByKeys(ctx context.Context, pk, sk string) error {
	coll := i.client.Database(i.database).Collection(i.collection)

	query := bson.M{"pk": pk}
	if sk != "" {
		query["sk"] = sk
	}

	result, err := coll.DeleteOne(ctx, query)
	if err != nil {
		return fmt.Errorf("delete document: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("document not found")
	}
	return nil
}
