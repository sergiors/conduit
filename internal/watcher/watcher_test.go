package watcher

import (
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
