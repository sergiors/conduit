package watcher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sergiors/relay/internal/redis"
	"github.com/sergiors/relay/internal/streams"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Watcher watches a single MongoDB collection for changes
type Watcher struct {
	mongoClient *mongo.Client
	database    string
	tableName   string
	oldImage    bool
	resumeToken string
	redisClient *redis.Client

	// Runtime state
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	isRunning atomic.Bool

	// Stats
	stats WatcherStats
	mu    sync.RWMutex
}

// NewWatcher creates a new watcher for a table
func NewWatcher(
	mongoClient *mongo.Client,
	database string,
	tableName string,
	oldImage bool,
	resumeToken string,
	redisClient *redis.Client,
) *Watcher {
	return &Watcher{
		mongoClient: mongoClient,
		database:    database,
		tableName:   tableName,
		oldImage:    oldImage,
		resumeToken: resumeToken,
		redisClient: redisClient,
		stats: WatcherStats{
			StartTime: time.Now(),
		},
	}
}

// Start begins watching for changes
func (w *Watcher) Start(ctx context.Context, handler func(streams.StreamRecord) error) error {
	if w.isRunning.Load() {
		return fmt.Errorf("watcher already running")
	}

	w.ctx, w.cancel = context.WithCancel(ctx)
	w.isRunning.Store(true)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.watchLoop(handler)
	}()

	log.Printf("Watcher started for table: %s", w.tableName)
	return nil
}

// Stop stops the watcher gracefully
func (w *Watcher) Stop(ctx context.Context) error {
	if !w.isRunning.Load() {
		return nil
	}

	log.Printf("Stopping watcher for table: %s", w.tableName)
	w.cancel()

	// Wait for goroutine to finish with timeout
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		w.isRunning.Store(false)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return fmt.Errorf("watcher stop timeout")
	}
}

// watchLoop is the main watch loop with resume token management
func (w *Watcher) watchLoop(handler func(streams.StreamRecord) error) {
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
			if err := w.watchOnce(handler); err != nil {
				// Check if this is a drop/invalidate error (expected when collection is removed)
				if err.Error() == "collection dropped" || err.Error() == "change stream invalidated" {
					log.Printf("Stopping watcher for %s: %v", w.tableName, err)
					return
				}

				w.recordError(err)

				// Skip token cleanup if context is already cancelled (watcher is stopping)
				if w.ctx.Err() == nil {
					// Invalidate resume token on error
					if delErr := w.redisClient.DeleteResumeToken(w.ctx, w.tableName); delErr != nil {
						log.Printf("Failed to delete resume token: %v", delErr)
					}

					// Wait before retrying
					select {
					case <-w.ctx.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}
		}
	}
}

// watchOnce runs a single watch session
func (w *Watcher) watchOnce(handler func(streams.StreamRecord) error) error {
	// Build change stream options
	opts := options.ChangeStream()
	opts.SetFullDocument(options.UpdateLookup)

	if w.oldImage {
		// Use WhenAvailable instead of Required to allow delete events
		// Required would fail if pre-image is not found (e.g., for deletes)
		opts.SetFullDocumentBeforeChange(options.WhenAvailable)
	}

	// Set resume token if available
	if w.resumeToken != "" {
		var resumeToken bson.Raw
		if err := bson.Unmarshal([]byte(w.resumeToken), &resumeToken); err == nil {
			opts.SetResumeAfter(resumeToken)
		}
	}

	// Get collection
	coll := w.mongoClient.Database(w.database).Collection(w.tableName)

	// Start change stream
	cursor, err := coll.Watch(w.ctx, mongo.Pipeline{}, opts)
	if err != nil {
		return fmt.Errorf("start change stream: %w", err)
	}
	defer cursor.Close(w.ctx)

	// Process changes
	for cursor.Next(w.ctx) {
		var change bson.M
		if err := cursor.Decode(&change); err != nil {
			w.recordError(fmt.Errorf("decode change: %w", err))
			continue
		}

		// Parse change into stream record
		record, err := w.parseChange(change)
		if err != nil {
			w.recordError(fmt.Errorf("parse change: %w", err))
			continue
		}

		// Call handler
		if err := handler(record); err != nil {
			w.recordError(fmt.Errorf("handle event: %w", err))
			// Don't return here - let retry logic handle it
		}

		// Update resume token after successful processing
		if cursor.ResumeToken() != nil {
			tokenData, err := bson.Marshal(cursor.ResumeToken())
			if err == nil {
				w.resumeToken = string(tokenData)
				if err := w.redisClient.SetResumeToken(w.ctx, w.tableName, w.resumeToken); err != nil {
					log.Printf("Failed to save resume token: %v", err)
				}
			}
		}

		// Update stats
		w.mu.Lock()
		w.stats.EventsProcessed++
		w.mu.Unlock()
	}

	if err := cursor.Err(); err != nil {
		if err == context.Canceled {
			return nil // Expected when watcher is stopped
		}
		return fmt.Errorf("cursor error: %w", err)
	}

	return cursor.Close(w.ctx)
}

// parseChange converts a MongoDB change event to a StreamRecord
func (w *Watcher) parseChange(change bson.M) (streams.StreamRecord, error) {
	opType, ok := change["operationType"].(string)
	if !ok {
		return streams.StreamRecord{}, fmt.Errorf("missing operationType")
	}

	record := streams.StreamRecord{
		TableName: w.tableName,
		Timestamp: time.Now(),
	}

	switch opType {
	case "insert":
		record.RecordType = streams.InsertRecord
		if doc, ok := change["fullDocument"].(bson.M); ok {
			record.NewImage = doc
		}
	case "update":
		record.RecordType = streams.ModifyRecord
		if doc, ok := change["fullDocument"].(bson.M); ok {
			record.NewImage = doc
		}
		if doc, ok := change["fullDocumentBeforeChange"].(bson.M); ok {
			record.OldImage = doc
		}
	case "replace":
		record.RecordType = streams.ModifyRecord
		if doc, ok := change["fullDocument"].(bson.M); ok {
			record.NewImage = doc
		}
		if doc, ok := change["fullDocumentBeforeChange"].(bson.M); ok {
			record.OldImage = doc
		}
	case "delete":
		record.RecordType = streams.RemoveRecord
		if doc, ok := change["fullDocumentBeforeChange"].(bson.M); ok {
			record.OldImage = doc
		}
	case "drop":
		// Collection was dropped - stop watcher
		log.Printf("Collection %s was dropped, stopping watcher", w.tableName)
		w.cancel()
		return streams.StreamRecord{}, fmt.Errorf("collection dropped")
	case "invalidate":
		// Change stream invalidated - collection likely dropped or renamed
		log.Printf("Change stream for %s invalidated, stopping watcher", w.tableName)
		w.cancel()
		return streams.StreamRecord{}, fmt.Errorf("change stream invalidated")
	default:
		// Ignore unknown operation types (renames, etc.)
		return streams.StreamRecord{}, fmt.Errorf("unknown operation type: %s", opType)
	}

	return record, nil
}

// recordError updates error stats
func (w *Watcher) recordError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.LastError = err
	w.stats.LastErrorTime = time.Now()
	log.Printf("Watcher error for %s: %v", w.tableName, err)
}

// GetStats returns current watcher statistics
func (w *Watcher) GetStats() WatcherStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.stats
}

// IsRunning returns true if the watcher is currently running
func (w *Watcher) IsRunning() bool {
	return w.isRunning.Load()
}

// OldImage returns the oldImage configuration
func (w *Watcher) OldImage() bool {
	return w.oldImage
}
