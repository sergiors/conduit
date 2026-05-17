package collections

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Document provides CRUD operations for documents in a MongoDB collection
type Document struct {
	client     *mongo.Client
	database   string
	collection string
}

// NewDocument creates a new document handler for a specific collection
func NewDocument(client *mongo.Client, database, collection string) *Document {
	return &Document{
		client:     client,
		database:   database,
		collection: collection,
	}
}

// DocumentQuery represents query parameters for scanning items
type DocumentQuery struct {
	Page   int64  `json:"page"`
	Limit  int64  `json:"limit"`
	Filter bson.M `json:"filter"`
	Sort   bson.M `json:"sort"`
}

// DocumentListResult represents the result of a scan operation
type DocumentListResult struct {
	Documents []bson.M `json:"documents"`
	Total      int64    `json:"total"`
	Page       int64    `json:"page"`
	Limit      int64    `json:"limit"`
	TotalPages int64    `json:"totalPages"`
}

// List returns documents with pagination
func (d *Document) List(ctx context.Context, query DocumentQuery) (*DocumentListResult, error) {
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

	coll := d.client.Database(d.database).Collection(d.collection)

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

	// Find documents
	opts := options.Find().
		SetSkip(skip).
		SetLimit(query.Limit).
		SetSort(query.Sort)

	cursor, err := coll.Find(ctx, query.Filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find documents: %w", err)
	}
	defer cursor.Close(ctx)

	var documents []bson.M
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode documents: %w", err)
	}

	if documents == nil {
		documents = []bson.M{}
	}

	return &DocumentListResult{
		Documents: documents,
		Total:      total,
		Page:       query.Page,
		Limit:      query.Limit,
		TotalPages: totalPages,
	}, nil
}

// Get retrieves a document by ID
func (d *Document) Get(ctx context.Context, id string) (bson.M, error) {
	coll := d.client.Database(d.database).Collection(d.collection)

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		// Try querying by string ID (pk/sk composite)
		result := coll.FindOne(ctx, bson.M{"_id": id})
		var doc bson.M
		if err := result.Decode(&doc); err != nil {
			if err == mongo.ErrNoDocuments {
				return nil, fmt.Errorf("document not found")
			}
			return nil, fmt.Errorf("find document: %w", err)
		}
		return doc, nil
	}

	result := coll.FindOne(ctx, bson.M{"_id": objectID})
	var doc bson.M
	if err := result.Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("document not found")
		}
		return nil, fmt.Errorf("find document: %w", err)
	}
	return doc, nil
}


// Create inserts a new item
func (d *Document) Create(ctx context.Context, data bson.M) (bson.M, error) {
	coll := d.client.Database(d.database).Collection(d.collection)

	result, err := coll.InsertOne(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("insert document: %w", err)
	}

	// Add _id to returned document
	data["_id"] = result.InsertedID
	return data, nil
}

// Update updates an existing item by ID
func (d *Document) Update(ctx context.Context, id string, data bson.M) (bson.M, error) {
	coll := d.client.Database(d.database).Collection(d.collection)

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
func (d *Document) Delete(ctx context.Context, id string) error {
	coll := d.client.Database(d.database).Collection(d.collection)

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
