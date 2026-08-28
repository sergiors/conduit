package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestWatcherCreation(t *testing.T) {
	t.Run("new watcher with correct configuration", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", "pk", "sk", true, "", nil)

		assert.NotNil(t, watcher)
		assert.Equal(t, "conduit", watcher.database)
		assert.Equal(t, "users", watcher.collectionName)
		assert.True(t, watcher.oldImage)
		assert.Equal(t, "", watcher.resumeToken)
	})

	t.Run("watcher with resume token", func(t *testing.T) {
		token := "test-resume-token"
		watcher := NewWatcher(nil, "conduit", "orders", "pk", "sk", false, token, nil)

		assert.NotNil(t, watcher)
		assert.Equal(t, token, watcher.resumeToken)
	})
}

func TestWatcherStats(t *testing.T) {
	t.Run("initial stats are correct", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", "pk", "sk", false, "", nil)

		stats := watcher.GetStats()
		assert.Zero(t, stats.EventsProcessed)
		assert.Nil(t, stats.LastError)
		assert.True(t, !stats.StartTime.IsZero())
	})

	t.Run("IsRunning returns false before start", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", "pk", "sk", false, "", nil)
		assert.False(t, watcher.IsRunning())
	})
}

func TestParseChange(t *testing.T) {
	t.Run("parse insert operation", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", "pk", "sk", false, "", nil)

		change := bson.M{
			"operationType": "insert",
			"fullDocument": bson.M{
				"_id":   "123",
				"name":  "John",
				"email": "john@example.com",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.InsertRecord, record.RecordType)
		assert.Equal(t, "users", record.TableName)
		assert.NotNil(t, record.NewImage)
		assert.Nil(t, record.OldImage)
	})

	t.Run("parse update operation with old image", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "orders", "pk", "sk", true, "", nil)

		change := bson.M{
			"operationType": "update",
			"fullDocument": bson.M{
				"_id":    "456",
				"status": "shipped",
			},
			"fullDocumentBeforeChange": bson.M{
				"_id":    "456",
				"status": "pending",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.ModifyRecord, record.RecordType)
		assert.NotNil(t, record.NewImage)
		assert.NotNil(t, record.OldImage)
	})

	t.Run("parse delete operation", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "sessions", "pk", "sk", true, "", nil)

		change := bson.M{
			"operationType": "delete",
			"fullDocumentBeforeChange": bson.M{
				"_id":  "789",
				"user": "john",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.RemoveRecord, record.RecordType)
		assert.Nil(t, record.NewImage)
		assert.NotNil(t, record.OldImage)
	})

	t.Run("parse unknown operation type", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", "pk", "sk", false, "", nil)

		change := bson.M{
			"operationType": "unknown",
		}

		record, err := watcher.parseChange(change)
		assert.Error(t, err)
		assert.Equal(t, streams.StreamRecord{}, record)
	})

	t.Run("parse missing operation type", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", "pk", "sk", false, "", nil)

		change := bson.M{}

		record, err := watcher.parseChange(change)
		assert.Error(t, err)
		assert.Equal(t, streams.StreamRecord{}, record)
	})

	t.Run("event ID is derived from resume token", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", "pk", "sk", false, "", nil)

		change := bson.M{
			"_id": bson.M{
				"_data": "826A91ADB6000000022B042C0100296E",
			},
			"operationType": "insert",
			"fullDocument":  bson.M{"_id": "123"},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, "users:826A91ADB6000000022B042C0100296E", record.EventID)
	})

	t.Run("event ID is stable across restarts", func(t *testing.T) {
		// Two independent watcher instances (a restart) parsing the same
		// change event must produce the same event ID.
		change := bson.M{
			"_id": bson.M{
				"_data": "826A91ADB6000000022B042C0100296E",
			},
			"operationType": "update",
			"fullDocument":  bson.M{"_id": "123"},
		}

		first := NewWatcher(nil, "conduit", "orders", "pk", "sk", false, "", nil)
		second := NewWatcher(nil, "conduit", "orders", "pk", "sk", false, "", nil)

		r1, err := first.parseChange(change)
		assert.NoError(t, err)
		r2, err := second.parseChange(change)
		assert.NoError(t, err)

		assert.Equal(t, r1.EventID, r2.EventID)
		assert.NotEmpty(t, r1.EventID)
	})

	t.Run("event ID falls back to clusterTime and documentKey", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "orders", "pk", "sk", false, "", nil)

		change := bson.M{
			"operationType": "update",
			"clusterTime":   primitive.Timestamp{T: 1787932086, I: 4},
			"documentKey":   bson.M{"_id": "456"},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, `orders:1787932086:4:{"_id":"456"}`, record.EventID)
	})

	t.Run("event ID fallback is stable across restarts", func(t *testing.T) {
		change := bson.M{
			"operationType": "delete",
			"clusterTime":   primitive.Timestamp{T: 1787932086, I: 7},
			"documentKey":   bson.M{"_id": "789"},
		}

		first := NewWatcher(nil, "conduit", "sessions", "pk", "sk", false, "", nil)
		second := NewWatcher(nil, "conduit", "sessions", "pk", "sk", false, "", nil)

		r1, err := first.parseChange(change)
		assert.NoError(t, err)
		r2, err := second.parseChange(change)
		assert.NoError(t, err)

		assert.Equal(t, r1.EventID, r2.EventID)
		assert.NotEmpty(t, r1.EventID)
	})

	t.Run("different changes produce different event IDs", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", "pk", "sk", false, "", nil)

		first, err := watcher.parseChange(bson.M{
			"_id":           bson.M{"_data": "826A91ADB6000000022B04"},
			"operationType": "insert",
		})
		assert.NoError(t, err)

		second, err := watcher.parseChange(bson.M{
			"_id":           bson.M{"_data": "826A91ADB6000000032B04"},
			"operationType": "insert",
		})
		assert.NoError(t, err)

		assert.NotEqual(t, first.EventID, second.EventID)
	})

	t.Run("event ID is never derived from time.Now", func(t *testing.T) {
		// Parsing the same change twice within one watcher must also be
		// deterministic (guards against any wall-clock dependency).
		watcher := NewWatcher(nil, "conduit", "users", "pk", "sk", false, "", nil)

		change := bson.M{
			"_id":           bson.M{"_data": "826A91ADB6000000022B04"},
			"operationType": "insert",
		}

		r1, err := watcher.parseChange(change)
		assert.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
		r2, err := watcher.parseChange(change)
		assert.NoError(t, err)

		assert.Equal(t, r1.EventID, r2.EventID)
	})
}

func TestIsResumeTokenInvalid(t *testing.T) {
	t.Run("token-fatal server codes are invalidating", func(t *testing.T) {
		// Verified against a live MongoDB 8.x replica set:
		//   9     -> FailedToParse: "resume token string was not a valid hex string"
		//   50811 -> KeyString format error (structurally invalid token)
		//
		// Note: stale-but-structurally-valid tokens (e.g. after a collection
		// drop) do NOT error at Watch() on MongoDB 8.x; the stream delivers a
		// `drop` event then `invalidate`, handled as terminal conditions. So
		// only parse failures of the token itself invalidate here.
		for _, code := range []int32{9, 50811} {
			err := mongo.CommandError{Code: code, Message: "synthetic"}
			assert.True(t, isResumeTokenInvalid(err), "code %d should invalidate", code)
		}
	})

	t.Run("wrapped token-fatal errors are still detected", func(t *testing.T) {
		cmdErr := mongo.CommandError{Code: 50811, Message: "KeyString format error: Unknown type: 255"}
		wrapped := fmt.Errorf("start change stream: %w", cmdErr)
		assert.True(t, isResumeTokenInvalid(wrapped))
	})

	t.Run("transient errors never invalidate the token", func(t *testing.T) {
		transient := []error{
			errors.New("connection refused"),
			context.DeadlineExceeded,
			io.EOF,
			mongo.CommandError{Code: 6, Message: "HostUnreachable"},
			mongo.CommandError{Code: 89, Message: "NetworkTimeout"},
			mongo.CommandError{Code: 91, Message: "ShutdownInProgress"},
			mongo.CommandError{Code: 189, Message: "PrimarySteppedDown"},
			mongo.CommandError{Code: 262, Message: "ExceededTimeLimit"},
			mongo.CommandError{Code: 43, Message: "CursorNotFound"},
			fmt.Errorf("start change stream: %w", errors.New("server selection error: context deadline exceeded")),
		}
		for _, err := range transient {
			assert.False(t, isResumeTokenInvalid(err), "error %v must not invalidate", err)
		}
	})

	t.Run("structurally valid tokens are never classified invalid", func(t *testing.T) {
		// A stale token from a dropped collection is structurally valid; on
		// MongoDB 8.x it does not error at Watch() — the stream delivers
		// `drop` then `invalidate` instead (verified live). It must not be
		// classified as token-invalid, so the token survives for a future
		// recreate of the same collection.
		validShape := bson.M{"_data": "826A91ADB6000000022B042C0100296E5A1004210D2489479F4AF0A8FC2ABF942361A6463C6F7065726174696F6E54797065003C696E7365727400000004"}
		stored, err := bson.Marshal(validShape)
		assert.NoError(t, err)
		assert.False(t, isResumeTokenInvalid(fmt.Errorf("start change stream: %w", errStaleTokenScenario(stored))))
	})

	t.Run("nil error is not invalidating", func(t *testing.T) {
		assert.False(t, isResumeTokenInvalid(nil))
	})
}

// errStaleTokenScenario wraps a sentinel so the classifier sees a generic
// error carrying a token-looking message; it must still not invalidate.
func errStaleTokenScenario(token []byte) error {
	return errors.New("change stream error for token " + string(token))
}

func TestResumeTokenPreservation(t *testing.T) {
	t.Run("terminal parse errors propagate out of watchOnce", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", "pk", "sk", false, "", nil)

		// A drop event must surface from parseChange instead of being
		// swallowed, so watchLoop can stop the watcher.
		_, err := watcher.parseChange(bson.M{"operationType": "drop"})
		assert.ErrorIs(t, err, errCollectionDropped)

		_, err = watcher.parseChange(bson.M{"operationType": "invalidate"})
		assert.ErrorIs(t, err, errChangeStreamInvalidated)
	})

	t.Run("sentinel errors are stable and distinct", func(t *testing.T) {
		assert.NotEqual(t, errCollectionDropped, errChangeStreamInvalidated)
		assert.EqualError(t, errCollectionDropped, "collection dropped")
		assert.EqualError(t, errChangeStreamInvalidated, "change stream invalidated")
	})
}

func TestManagerCreation(t *testing.T) {
	t.Run("new manager with correct configuration", func(t *testing.T) {
		cfg := DefaultConfig()
		manager := NewManager(nil, "conduit", nil, nil, nil, nil, cfg)

		assert.NotNil(t, manager)
		assert.Equal(t, "conduit", manager.database)
		assert.Equal(t, 30*time.Second, manager.syncInterval)
		assert.Equal(t, 0, manager.GetActiveWatchers())
	})
}

func TestSinkIdentity(t *testing.T) {
	t.Run("sink identity is the persisted ID", func(t *testing.T) {
		sink := collections.Sink{
			ID:   "507f1f77bcf86cd799439011",
			Type: collections.SinkTypeHTTP,
			Spec: map[string]interface{}{"endpoint": "https://webhook.example.com"},
		}
		assert.Equal(t, "507f1f77bcf86cd799439011", sink.Identity())
	})
}

func TestManagerConfig(t *testing.T) {
	t.Run("default config has sensible values", func(t *testing.T) {
		cfg := DefaultConfig()
		assert.Equal(t, 30*time.Second, cfg.SyncInterval)
	})

	t.Run("custom config", func(t *testing.T) {
		cfg := Config{
			SyncInterval: 60 * time.Second,
		}
		assert.Equal(t, 60*time.Second, cfg.SyncInterval)
	})
}
