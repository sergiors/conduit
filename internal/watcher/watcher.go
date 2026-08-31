package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	recoverpkg "github.com/sergiors/conduit/internal/recover"
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
// Two categories justify deleting the stored token (verified against MongoDB
// 8.x):
//   - The token itself is unusable:
//   - code 9 "FailedToParse": token is not valid hex ("resume token string
//     was not a valid hex string").
//   - code 50811 "KeyString format error": token is structurally invalid.
//   - The resume point predates the oplog window:
//   - code 286 "ChangeStreamHistoryLost": the history the token points to
//     is gone (e.g. the worker was down longer than the oplog preserves
//     history). The token is not malformed, but keeping it would stall the
//     watcher forever on a doomed resume, so it is invalidated and the
//     stream restarts from the current position. The events between the old
//     token and the oplog window start are unrecoverable either way, so this
//     trades unrecoverable history for forward progress.
//   - "cannot resume stream; the resume token was not found" (message match
//     only): MongoDB 8.2 wraps this condition in code 280
//     "ChangeStreamFatalError" at Watch/aggregate time — verified live by
//     resuming with a structurally valid forged token whose clusterTime
//     predates the oplog (twice reproduced). Code 280 is a wrapper for
//     several fatal stream conditions and is NOT matched by itself; the
//     message substring is specific to the missing-resume-point failure.
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
		case 286: // ChangeStreamHistoryLost: resume point predates the oplog window
			return true
		}
	}

	// String matching as a last resort for errors that may lose the typed
	// error through wrapping. These messages only ever originate from the
	// server rejecting the token itself. The missing-resume-point case is
	// message-matched rather than code-matched because MongoDB 8.2 delivers it
	// as code 280 (ChangeStreamFatalError, a wrapper for multiple fatal
	// conditions) with the message below (verified live); matching the
	// unambiguous substring avoids both the forever-stall this causes and
	// false positives from other 280-wrapped conditions.
	msg := err.Error()
	return strings.Contains(msg, "resume token string was not a valid hex string") ||
		strings.Contains(msg, "KeyString format error") ||
		strings.Contains(msg, "cannot resume stream; the resume token was not found")
}

// Watcher watches a single MongoDB collection for changes
type Watcher struct {
	mongoClient    *mongo.Client
	database       string
	collectionName string
	oldImage       bool
	resumeToken    string
	// startAtOperationTime is the first-start checkpoint captured at stream
	// enablement; it anchors the stream only when no resume token exists.
	startAtOperationTime *primitive.Timestamp
	redisClient          RedisClient

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
	oldImage bool,
	resumeToken string,
	startAtOperationTime *primitive.Timestamp,
	redisClient RedisClient,
) *Watcher {
	return &Watcher{
		mongoClient:          mongoClient,
		database:             database,
		collectionName:       collectionName,
		oldImage:             oldImage,
		resumeToken:          resumeToken,
		startAtOperationTime: startAtOperationTime,
		redisClient:          redisClient,
		stats: WatcherStats{
			StartTime: time.Now(),
		},
	}
}

// Start begins watching for changes.
//
// Settlement contract for handler: it is invoked once per change event and
// must return nil only when the event is settled — either successfully
// dispatched to the sinks or durably persisted into the retry queue. When it
// returns a non-nil error (e.g. the event was neither delivered nor queued,
// or a downstream panic was converted to an error), the watcher treats the
// event as unsettled: it does NOT advance the resume token and, after a 5s
// backoff, reopens the change stream from the last settled position so the
// unsettled event is replayed. Returning nil guarantees the event will not be
// re-delivered; returning an error guarantees it will be.
//
// Lifecycle contract for IsRunning: isRunning reflects whether the watch
// goroutine is alive. It is set true when the goroutine is spawned and set
// false unconditionally when the goroutine exits — whether by a recovered
// panic, a terminal condition (collection dropped / change stream invalidated),
// an exhausted retry loop, or a normal context cancellation. The manager's
// reconciliation treats IsRunning()==false as "watcher absent" and recreates
// it, so a watcher that exits for any reason is recovered on the next sync.
func (w *Watcher) Start(ctx context.Context, handler func(streams.StreamRecord) error) error {
	if w.isRunning.Load() {
		return fmt.Errorf("watcher already running")
	}

	w.ctx, w.cancel = context.WithCancel(ctx)
	w.isRunning.Store(true)

	w.wg.Add(1)
	go func() {
		// Backstop: a panic anywhere in the watch loop (e.g. inside a handler
		// that slips past per-event isolation) would otherwise crash the
		// process. Protect catches it on this goroutine so the deferred
		// wg.Done still runs and Stop's Wait cannot hang.
		//
		// isRunning is cleared on EVERY goroutine exit, not only on a panic:
		// watchLoop also returns on terminal conditions (drop/invalidate) and
		// on context cancellation, and in all those cases the watcher is dead
		// and must be reconciled by the manager. Clearing the flag here (rather
		// than only in the panic branch) makes IsRunning truthful in every exit
		// path, so the manager's sync recreates the watcher.
		defer w.wg.Done()
		// The panic payload is already logged by Protect; the panic matters
		// only in that it also exits the goroutine, and the unconditional
		// isRunning clear below handles both panic and normal exits alike.
		recoverpkg.Protect("watcher:"+w.collectionName, func() {
			w.watchLoop(handler)
		})
		w.isRunning.Store(false)
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
					log.Printf("Watcher for %s exiting (terminal condition: %v); the manager's sync will recreate it if the collection is still enabled", w.collectionName, err)
					return
				}

				w.recordError(err)

				// Only invalidate when MongoDB itself rejects the resume
				// token: the token itself is unusable (9/50811) or the resume
				// point is outside the oplog window (286). Deleting it on
				// generic errors would silently skip every event that
				// occurred while the watcher was down.
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

// buildChangeStreamOptions constructs the change stream options for a watch
// session.
//
// Resume-position policy (priority, high to low):
//   - resume token  : when set, resumeAfter anchors the stream exactly at the
//     last settled event. This is the steady-state path and always wins.
//   - first-start checkpoint : when NO token exists but a startAtOperationTime
//     checkpoint was captured at stream enablement (collection.stream_started_at),
//     SetStartAtOperationTime anchors the stream at enablement, so every event
//     committed at/after enablement is streamed — closing the enable → first
//     watcher-start window where a plain fresh stream would start at "now" and
//     silently skip those events.
//   - neither : a fresh stream starting at the current position (legacy
//     behavior for collections enabled long before the worker started, or a
//     token-invalidated restart with no checkpoint).
//
// A resume token is never overridden by the checkpoint: the checkpoint's only
// job is to cover the first-start gap and post-invalidate fresh sessions.
// After the first settled event, per-event token persistence takes over.
func (w *Watcher) buildChangeStreamOptions() *options.ChangeStreamOptions {
	opts := options.ChangeStream()
	opts.SetFullDocument(options.UpdateLookup)

	if w.oldImage {
		opts.SetFullDocumentBeforeChange(options.WhenAvailable)
	}

	if w.resumeToken != "" {
		var resumeToken bson.Raw
		if err := bson.Unmarshal([]byte(w.resumeToken), &resumeToken); err != nil {
			// A corrupt stored token behaves as absent: fall through to the
			// checkpoint or a fresh stream instead of stalling on a doomed
			// resume.
			log.Printf("Resume token for %s is unparseable, falling through to checkpoint/fresh stream: %v", w.collectionName, err)
		} else {
			opts.SetResumeAfter(resumeToken)
			return opts
		}
	}

	// No token: anchor at the enablement checkpoint when one exists.
	if w.startAtOperationTime != nil {
		opts.SetStartAtOperationTime(w.startAtOperationTime)
	}

	return opts
}

// watchOnce runs a single watch session
func (w *Watcher) watchOnce(handler func(streams.StreamRecord) error) error {
	opts := w.buildChangeStreamOptions()

	// Get collection
	coll := w.mongoClient.Database(w.database).Collection(w.collectionName)

	// Start change stream
	cursor, err := coll.Watch(w.ctx, mongo.Pipeline{}, opts)
	if err != nil {
		return fmt.Errorf("start change stream: %w", err)
	}

	// A detached, bounded context: at shutdown w.ctx is already cancelled, and
	// cursor.Close(cancelledCtx) never sends killCursors to the server,
	// leaking the cursor.
	closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(w.ctx), 5*time.Second)
	defer func() {
		closeCancel()
		_ = cursor.Close(closeCtx)
	}()

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
				// Persist the terminal event's own resume token before exiting:
				// it was seen and handled, so the next session must resume
				// after it — otherwise a historical drop replayed onto a
				// recreated collection terminates the watcher on every sync
				// (recreation restarts from the pre-drop token, forever).
				w.persistTerminalToken(cursor.ResumeToken())
				return err
			}
			w.recordError(fmt.Errorf("parse change: %w", err))
			continue
		}

		// Process the event. processEvent advances the resume token only when
		// the event is settled; otherwise it leaves the token untouched and
		// returns an error so watchLoop backs off and reopens the stream from
		// the last settled position, replaying the unsettled event.
		if err := w.processEvent(handler, record, cursor.ResumeToken()); err != nil {
			return err
		}
	}

	if err := cursor.Err(); err != nil {
		if err == context.Canceled {
			return nil // Expected when watcher is stopped
		}
		return fmt.Errorf("cursor error: %w", err)
	}

	return nil
}

// processEvent runs a single change event through the handler and, if and only
// if the event is settled, advances the resume token.
//
// Settlement contract: the handler returns nil only when the event is settled
// — either delivered to the sinks or durably queued for retry (see Start's doc
// comment and ErrEventUnsettled). When the handler returns an error, the event
// is unsettled: the in-memory resumeToken and the Redis copy are left
// untouched, and processEvent returns an error so watchOnce exits and watchLoop
// backs off 5s then reopens the stream from the last settled position,
// replaying this event on the next session.
//
// The resumed position passed in (the cursor's current resume token) is
// persisted best-effort with a detached bounded context so a shutdown that
// cancels w.ctx mid-event cannot drop an already-earned token advance.
func (w *Watcher) processEvent(handler func(streams.StreamRecord) error, record streams.StreamRecord, token bson.Raw) error {
	// Call handler. Any returned error means the event was neither delivered
	// nor persisted (a dispatch-then-enqueue failure surfacing as
	// ErrEventUnsettled, a handler-internal redis failure, or a panic
	// converted to an error by invokeHandler). In that case the resume token
	// must NOT advance so the change stream replays the event.
	if err := w.invokeHandler(handler, record); err != nil {
		w.recordError(fmt.Errorf("handle event (event %s in %s not settled): %w", record.EventID, w.collectionName, err))
		return fmt.Errorf("handle event: %w", err)
	}

	// Event is settled. Advance the resume token: the in-memory token first so
	// an in-memory restart never replays the event; the Redis copy is updated
	// best-effort so a process restart resumes from here too.
	if token != nil {
		tokenData, err := bson.Marshal(token)
		if err == nil {
			w.resumeToken = string(tokenData)
			bkctx, bkCancel := context.WithTimeout(context.WithoutCancel(w.ctx), 5*time.Second)
			err := w.redisClient.SetResumeToken(bkctx, w.collectionName, w.resumeToken)
			bkCancel()
			if err != nil {
				log.Printf("Failed to save resume token: %v", err)
			}
		}
	}

	// Update stats: EventsProcessed counts settled events only, so it reflects
	// events that were delivered or durably queued for retry.
	w.mu.Lock()
	w.stats.EventsProcessed++
	w.mu.Unlock()

	return nil
}

// invokeHandler calls the event handler, converting a panic inside it into a
// returned error. A panicking handler (e.g. a nil field deref or a marshal
// panic deep in the dispatch path) must not kill the watch loop; the event is
// left undelivered, so it is unsettled and the resume token must not advance.
// Per the settlement contract in Start/doc.go, any returned error here —
// including panic-converted ones — means the event is unsettled.
func (w *Watcher) invokeHandler(handler func(streams.StreamRecord) error, record streams.StreamRecord) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
			log.Printf("Watcher %s: handler panic (event unsettled, event remains undelivered): %v\n%s", w.collectionName, r, debug.Stack())
		}
	}()
	return handler(record)
}

// persistTerminalToken saves the terminal event's own resume position so the
// next session resumes after it instead of replaying the drop/invalidate from
// the pre-drop position.
func (w *Watcher) persistTerminalToken(token bson.Raw) {
	if token == nil {
		return
	}
	tokenData, err := bson.Marshal(token)
	if err != nil {
		log.Printf("Failed to marshal terminal resume token for %s: %v", w.collectionName, err)
		return
	}

	w.resumeToken = string(tokenData)

	// redisClient may be nil in tests; skip the durable write in that case.
	if w.redisClient == nil {
		return
	}
	bkctx, bkCancel := context.WithTimeout(context.WithoutCancel(w.ctx), 5*time.Second)
	err = w.redisClient.SetResumeToken(bkctx, w.collectionName, w.resumeToken)
	bkCancel()
	if err != nil {
		log.Printf("Failed to save terminal resume token for %s: %v", w.collectionName, err)
	}
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

	// The documentKey is present in insert/update/replace/delete change events
	// and carries the MongoDB `_id`. It is the source of the record's document
	// identity (see documentID), applied to all four operation types.
	documentKey, _ := change["documentKey"].(bson.M)

	// The document identity is the MongoDB `_id` from the change event's
	// documentKey, available for every operation type.
	record.DocumentID = w.documentID(documentKey)

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

// documentID extracts the deterministic document identity for a change event:
// the MongoDB `_id` from the change event's documentKey, stringified by
// stringifyID (ObjectID -> hex, string -> verbatim, other -> JSON). Every
// insert/update/replace/delete change event carries documentKey, so this
// identity is always available for the record's documentId field.
func (w *Watcher) documentID(documentKey bson.M) string {
	if idVal, ok := documentKey["_id"]; ok {
		return stringifyID(idVal)
	}
	return ""
}

// stringifyID renders a MongoDB `_id` value as a deterministic string:
// ObjectID -> hex, string -> verbatim, anything else -> its JSON
// representation (the deterministic BSON->JSON form).
func stringifyID(v interface{}) string {
	switch t := v.(type) {
	case primitive.ObjectID:
		return t.Hex()
	case string:
		return t
	default:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", v)
	}
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

// eventID derives a stable identifier for a change event, used as the
// idempotency key for delivery. It is derived exclusively from the MongoDB
// change event itself — never from wall-clock time — so the same change always
// yields the same ID, including after a process restart replays the event.
//
// The MongoDB resume token (change event `_id._data`) is the primary source:
// it is a hex string that uniquely identifies the change (it encodes
// clusterTime, operation type and document key) and is identical when the
// event is re-read from the oplog. If the token is missing or malformed,
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
