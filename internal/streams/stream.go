package streams

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// RecordType represents the type of stream record
type RecordType string

const (
	InsertRecord RecordType = "INSERT"
	ModifyRecord RecordType = "MODIFY"
	RemoveRecord RecordType = "REMOVE"
)

// StreamRecord represents a DynamoDB-style stream record
type StreamRecord struct {
	TableName  string      `json:"tableName"`
	RecordType RecordType  `json:"recordType"`
	NewImage   interface{} `json:"newImage,omitempty"`
	OldImage   interface{} `json:"oldImage,omitempty"`
	Timestamp  time.Time   `json:"timestamp"`
}

// Watcher watches a MongoDB collection for changes (DynamoDB Streams style)
type Watcher struct {
	client   *mongo.Client
	database string
	table    string
	oldImage bool
}

// NewWatcher creates a new table watcher
func NewWatcher(client *mongo.Client, database, table string, oldImage bool) *Watcher {
	return &Watcher{
		client:   client,
		database: database,
		table:    table,
		oldImage: oldImage,
	}
}

// Watch starts watching for changes on the table
// Returns a channel of stream records and an error channel
func (w *Watcher) Watch(ctx context.Context) (<-chan StreamRecord, <-chan error, error) {
	records := make(chan StreamRecord)
	errs := make(chan error, 1)

	go func() {
		defer close(records)
		defer close(errs)
		w.watchLoop(ctx, records, errs)
	}()

	return records, errs, nil
}

func (w *Watcher) watchLoop(ctx context.Context, records chan<- StreamRecord, errs chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if err := w.watchOnce(ctx, records); err != nil {
				select {
				case errs <- fmt.Errorf("watch error: %w", err):
				default:
				}
				// Wait before retrying
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}
}

func (w *Watcher) watchOnce(ctx context.Context, records chan<- StreamRecord) error {
	// Build change stream options
	opts := options.ChangeStream()
	opts.SetFullDocument(options.UpdateLookup)

	if w.oldImage {
		opts.SetFullDocumentBeforeChange(options.Required)
	}

	// Get collection (table in DynamoDB terms)
	coll := w.client.Database(w.database).Collection(w.table)

	// Start change stream
	cursor, err := coll.Watch(ctx, mongo.Pipeline{}, opts)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	// Process changes
	for cursor.Next(ctx) {
		var change bson.M
		if err := cursor.Decode(&change); err != nil {
			return err
		}

		record, err := w.parseChange(change)
		if err != nil {
			continue // Skip malformed records
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case records <- record:
		}
	}

	return cursor.Err()
}

func (w *Watcher) parseChange(change bson.M) (StreamRecord, error) {
	opType := change["operationType"].(string)

	record := StreamRecord{
		TableName: w.table,
		Timestamp: time.Now(),
	}

	switch opType {
	case "insert":
		record.RecordType = InsertRecord
		if doc, ok := change["fullDocument"].(bson.M); ok {
			record.NewImage = doc
		}
	case "update":
		record.RecordType = ModifyRecord
		if doc, ok := change["fullDocument"].(bson.M); ok {
			record.NewImage = doc
		}
		if doc, ok := change["fullDocumentBeforeChange"].(bson.M); ok {
			record.OldImage = doc
		}
	case "replace":
		record.RecordType = ModifyRecord
		if doc, ok := change["fullDocument"].(bson.M); ok {
			record.NewImage = doc
		}
	case "delete":
		record.RecordType = RemoveRecord
		if doc, ok := change["fullDocumentBeforeChange"].(bson.M); ok {
			record.OldImage = doc
		}
	default:
		return StreamRecord{}, fmt.Errorf("unknown operation type: %s", opType)
	}

	return record, nil
}
