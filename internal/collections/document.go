package collections

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Document provides read-only access to documents in a MongoDB collection.
type Document struct {
	client     *mongo.Client
	database   string
	collection string
}

// NewDocument creates a new document reader for a specific collection.
func NewDocument(client *mongo.Client, database, collection string) *Document {
	return &Document{
		client:     client,
		database:   database,
		collection: collection,
	}
}

// DocumentListOptions controls the bounded result set returned by List.
// A zero value lists documents without a limit or skip.
type DocumentListOptions struct {
	// Limit caps the number of documents returned. Zero means no limit.
	Limit int64
	// Skip is the number of documents to skip before returning results.
	// It must be non-negative.
	Skip int64
}

// List returns documents in the collection, bounded by opts. Results are
// sorted by _id ascending for deterministic pagination. An empty result set
// is returned as an empty (non-nil) slice.
func (d *Document) List(ctx context.Context, opts DocumentListOptions) ([]bson.M, error) {
	coll := d.client.Database(d.database).Collection(d.collection)

	findOpts := options.Find().
		SetSort(bson.D{{Key: "_id", Value: 1}}).
		SetSkip(opts.Skip)
	if opts.Limit > 0 {
		findOpts.SetLimit(opts.Limit)
	}

	cursor, err := coll.Find(ctx, bson.M{}, findOpts)
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

	return documents, nil
}

// Get retrieves a document by ID. It first tries to parse the ID as an
// ObjectID and falls back to a string ID.
func (d *Document) Get(ctx context.Context, id string) (bson.M, error) {
	coll := d.client.Database(d.database).Collection(d.collection)

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		// Try querying by string ID (pk/sk composite)
		result := coll.FindOne(ctx, bson.M{"_id": id})
		var doc bson.M
		if err := result.Decode(&doc); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, ErrDocumentNotFound
			}
			return nil, fmt.Errorf("find document: %w", err)
		}
		return doc, nil
	}

	result := coll.FindOne(ctx, bson.M{"_id": objectID})
	var doc bson.M
	if err := result.Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrDocumentNotFound
		}
		return nil, fmt.Errorf("find document: %w", err)
	}
	return doc, nil
}
