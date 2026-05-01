package watcher

import (
	"testing"
	"time"

	"github.com/sergiors/relay/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

func TestWatcherCreation(t *testing.T) {
	t.Run("new watcher with correct configuration", func(t *testing.T) {
		watcher := NewWatcher(nil, "relay", "users", true, "", nil)

		assert.NotNil(t, watcher)
		assert.Equal(t, "relay", watcher.database)
		assert.Equal(t, "users", watcher.tableName)
		assert.True(t, watcher.oldImage)
		assert.Equal(t, "", watcher.resumeToken)
	})

	t.Run("watcher with resume token", func(t *testing.T) {
		token := "test-resume-token"
		watcher := NewWatcher(nil, "relay", "orders", false, token, nil)

		assert.NotNil(t, watcher)
		assert.Equal(t, token, watcher.resumeToken)
	})
}

func TestWatcherStats(t *testing.T) {
	t.Run("initial stats are correct", func(t *testing.T) {
		watcher := NewWatcher(nil, "relay", "test", false, "", nil)

		stats := watcher.GetStats()
		assert.Zero(t, stats.EventsProcessed)
		assert.Nil(t, stats.LastError)
		assert.True(t, !stats.StartTime.IsZero())
	})

	t.Run("IsRunning returns false before start", func(t *testing.T) {
		watcher := NewWatcher(nil, "relay", "test", false, "", nil)
		assert.False(t, watcher.IsRunning())
	})
}

func TestParseChange(t *testing.T) {
	t.Run("parse insert operation", func(t *testing.T) {
		watcher := NewWatcher(nil, "relay", "users", false, "", nil)

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
		watcher := NewWatcher(nil, "relay", "orders", true, "", nil)

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
		watcher := NewWatcher(nil, "relay", "sessions", true, "", nil)

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
		watcher := NewWatcher(nil, "relay", "test", false, "", nil)

		change := bson.M{
			"operationType": "unknown",
		}

		record, err := watcher.parseChange(change)
		assert.Error(t, err)
		assert.Equal(t, streams.StreamRecord{}, record)
	})

	t.Run("parse missing operation type", func(t *testing.T) {
		watcher := NewWatcher(nil, "relay", "test", false, "", nil)

		change := bson.M{}

		record, err := watcher.parseChange(change)
		assert.Error(t, err)
		assert.Equal(t, streams.StreamRecord{}, record)
	})
}

func TestManagerCreation(t *testing.T) {
	t.Run("new manager with correct configuration", func(t *testing.T) {
		cfg := DefaultConfig()
		manager := NewManager(nil, "relay", nil, nil, nil, nil, cfg)

		assert.NotNil(t, manager)
		assert.Equal(t, "relay", manager.database)
		assert.Equal(t, 30*time.Second, manager.syncInterval)
		assert.Equal(t, 0, manager.GetActiveWatchers())
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
