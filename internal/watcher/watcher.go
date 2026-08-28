package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/streams"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Sentinel errors for terminal change stream conditions.
var (
	errCollectionDropped       = errors.New("collection dropped")
	errChangeStreamInvalidated = errors.New("change stream invalidated")
)

// isResumeTokenInvalid reports whether an error from the change stream means
// MongoDB explicitly rejected the resume token as no longer usable.
//
// Only server-side parse failures of the token itself justify deleting the
// stored token (verified against MongoDB 8.x):
//   - code 9 "FailedToParse": token is not valid hex ("resume token string
//     was not a valid hex string").
//   - code 50811 "KeyString format error": token is structurally invalid.
//
// A stale-but-structurally-valid token (e.g. after a collection drop) is NOT
// treated as invalid: MongoDB delivers a `drop` event followed by
// `invalidate` on that stream (verified live), which the watcher handles as
// terminal conditions without deleting the token. Everything else — network
// failures, elections, cursor timeouts — is transient: the token is preserved
// and the watcher retries from the last successful position.
func isResumeTokenInvalid(err error) bool {
	if err == nil {
		return false
	}

	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) {
		switch cmdErr.Code {
		case 9: // FailedToParse: "resume token string was not a valid hex string"
			return true
		case 50811: // KeyString format error: token is structurally invalid
			return true
		}
	}

	// String matching as a last resort for errors that may lose the typed
	// error through wrapping. These messages only ever originate from the
	// server rejecting the token itself.
	msg := err.Error()
	return strings.Contains(msg, "resume token string was not a valid hex string") ||
		strings.Contains(msg, "KeyString format error")
}

// Watcher watches a single MongoDB collection for changes
type Watcher struct {
	mongoClient    *mongo.Client
	database       string
	collectionName string
	pkField        string
	skField        string
	oldImage       bool
	resumeToken    string
	redisClient    *redis.Client

	// Runtime state
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	isRunning atomic.Bool

	// Stats
	stats WatcherStats
	mu    sync.RWMutex
}

// NewWatcher creates a new watcher for a collection
func NewWatcher(
	mongoClient *mongo.Client,
	database string,
	collectionName string,
	pkField string,
	skField string,
	oldImage bool,
	resumeToken string,
	redisClient *redis.Client,
) *Watcher {
	return &Watcher{
		mongoClient:    mongoClient,
		database:       database,
		collectionName: collectionName,
		pkField:        pkField,
		skField:        skField,
		oldImage:       oldImage,
		resumeToken:    resumeToken,
		redisClient:    redisClient,
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

	log.Printf("Watcher started for collection: %s", w.collectionName)
	return nil
}

// Stop stops the watcher gracefully
func (w *Watcher) Stop(ctx context.Context) error {
	if !w.isRunning.Load() {
		return nil
	}

	log.Printf("Stopping watcher for collection: %s", w.collectionName)
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

// watchLoop is the main watch loop with resume token management.
//
// Resume token policy:
//   - The token is only advanced after an event is successfully handled.
//   - Transient errors (network failures, elections, cursor timeouts) never
//     touch the token: the loop retries from the last successful position.
//   - The stored token is invalidated only when MongoDB explicitly rejects it
//     as invalid (see isResumeTokenInvalid). It is never deleted as a side
//     effect of generic errors; a dropped/invalidated collection is handled
//     as a terminal condition instead.
func (w *Watcher) watchLoop(handler func(streams.StreamRecord) error) {
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
			if err := w.watchOnce(handler); err != nil {
				// Terminal conditions: the collection is gone or the change
				// stream was invalidated. Stop the watcher; the manager will
				// reconcile the watcher lifecycle.
				if errors.Is(err, errCollectionDropped) || errors.Is(err, errChangeStreamInvalidated) {
					log.Printf("Stopping watcher for %s: %v", w.collectionName, err)
					return
				}

				w.recordError(err)

				// Only invalidate when MongoDB itself rejects the resume
				// token. Deleting it on generic errors would silently skip
				// every event that occurred while the watcher was down.
				if isResumeTokenInvalid(err) {
					log.Printf("Resume token for %s rejected by MongoDB, invalidating: %v", w.collectionName, err)
					w.resumeToken = ""
					if delErr := w.redisClient.DeleteResumeToken(w.ctx, w.collectionName); delErr != nil {
						log.Printf("Failed to invalidate resume token: %v", delErr)
					}
				}

				// Wait before retrying. The token is preserved, so the next
				// watchOnce resumes from the last successfully processed
				// event and nothing is skipped.
				select {
				case <-w.ctx.Done():
					return
				case <-time.After(5 * time.Second):
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
	coll := w.mongoClient.Database(w.database).Collection(w.collectionName)

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
			// Terminal conditions (drop/invalidate) must abort the session so
			// watchLoop can stop the watcher. Unknown operation types are
			// skipped like any other non-terminal parse problem.
			if errors.Is(err, errCollectionDropped) || errors.Is(err, errChangeStreamInvalidated) {
				return err
			}
			w.recordError(fmt.Errorf("parse change: %w", err))
			continue
		}

		// Call handler
		if err := handler(record); err != nil {
			w.recordError(fmt.Errorf("handle event: %w", err))
			// Don't return here - let retry logic handle it
		}

		// Advance the resume token only after the event was handed off
		// successfully. The in-memory token advances first so an in-memory
		// restart never replays the event; the Redis copy is updated
		// best-effort so a process restart resumes from here too.
		if cursor.ResumeToken() != nil {
			tokenData, err := bson.Marshal(cursor.ResumeToken())
			if err == nil {
				w.resumeToken = string(tokenData)
				if err := w.redisClient.SetResumeToken(w.ctx, w.collectionName, w.resumeToken); err != nil {
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
		TableName: w.collectionName,
		Timestamp: time.Now(),
		EventID:   w.eventID(change),
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
		log.Printf("Collection %s was dropped, stopping watcher", w.collectionName)
		if w.cancel != nil {
			w.cancel()
		}
		return streams.StreamRecord{}, errCollectionDropped
	case "invalidate":
		// Change stream invalidated - collection likely dropped or renamed
		log.Printf("Change stream for %s invalidated, stopping watcher", w.collectionName)
		if w.cancel != nil {
			w.cancel()
		}
		return streams.StreamRecord{}, errChangeStreamInvalidated
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
	log.Printf("Watcher error for %s: %v", w.collectionName, err)
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

// eventID derives a stable identifier for a change event, used as the
// idempotency key for delivery. It is derived exclusively from the MongoDB
// change event itself — never from wall-clock time — so the same change always
// yields the same ID, including after a process restart replays the event.
//
// The MongoDB resume token (change event `_id._data`) is the primary source:
// it is a hex string that uniquely identifies the change (it encodes
// clusterTime, operation type and document key) and is identical when the
// event is re-read from the oplog. If the token is missing or malfo	rmed,
// clusterTime plus documentKey are used as a fallback, still sourced purely
// from change-stream data.
//
// Returns "" only when no change-stream identity data is present at all;
// the caller treats an empty ID as undeliverable for dedup purposes.
func (w *Watcher) eventID(change bson.M) string {
	// Preferred: the resume token's _data string.
	if idDoc, ok := change["_id"].(bson.M); ok {
		if data, ok := idDoc["_data"].(string); ok && data != "" {
			return fmt.Sprintf("%s:%s", w.collectionName, data)
		}
	}

	// Fallback: clusterTime (timestamp + increment) and documentKey.
	// Both are part of the change event and stable across replays.
	parts := make([]string, 0, 3)
	if ct, ok := change["clusterTime"].(primitive.Timestamp); ok {
		parts = append(parts, fmt.Sprintf("%d:%d", ct.T, ct.I))
	}
	if dk, ok := change["documentKey"].(bson.M); ok {
		if keyJSON, err := json.Marshal(dk); err == nil {
			parts = append(parts, string(keyJSON))
		}
	}
	if len(parts) > 0 {
		return fmt.Sprintf("%s:%s", w.collectionName, strings.Join(parts, ":"))
	}

	return ""
}
