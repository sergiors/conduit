package streams

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

func TestRecordTypeConstants(t *testing.T) {
	t.Run("record types match DynamoDB style", func(t *testing.T) {
		assert.Equal(t, RecordType("INSERT"), InsertRecord)
		assert.Equal(t, RecordType("MODIFY"), ModifyRecord)
		assert.Equal(t, RecordType("REMOVE"), RemoveRecord)
	})
}

func TestStreamRecord(t *testing.T) {
	t.Run("valid stream record structure", func(t *testing.T) {
		now := time.Now()
		record := StreamRecord{
			TableName:  "users",
			RecordType: InsertRecord,
			NewImage: bson.M{
				"_id":   "123",
				"name":  "John",
				"email": "john@example.com",
			},
			Timestamp: now,
		}

		assert.Equal(t, "users", record.TableName)
		assert.Equal(t, InsertRecord, record.RecordType)
		assert.NotNil(t, record.NewImage)
		assert.Nil(t, record.OldImage)
		assert.Equal(t, now, record.Timestamp)
	})

	t.Run("modify record with old image", func(t *testing.T) {
		record := StreamRecord{
			TableName:  "orders",
			RecordType: ModifyRecord,
			NewImage: bson.M{
				"_id":     "456",
				"status":  "shipped",
				"updated": "2024-01-01",
			},
			OldImage: bson.M{
				"_id":     "456",
				"status":  "pending",
				"updated": "2024-01-01",
			},
			Timestamp: time.Now(),
		}

		assert.Equal(t, ModifyRecord, record.RecordType)
		assert.NotNil(t, record.NewImage)
		assert.NotNil(t, record.OldImage)
	})

	t.Run("remove record only has old image", func(t *testing.T) {
		record := StreamRecord{
			TableName:  "sessions",
			RecordType: RemoveRecord,
			OldImage: bson.M{
				"_id":  "789",
				"user": "john",
			},
			Timestamp: time.Now(),
		}

		assert.Equal(t, RemoveRecord, record.RecordType)
		assert.Nil(t, record.NewImage)
		assert.NotNil(t, record.OldImage)
	})
}

func TestStreamRecordJSON(t *testing.T) {
	t.Run("marshal and unmarshal to JSON", func(t *testing.T) {
		original := StreamRecord{
			TableName:  "products",
			RecordType: ModifyRecord,
			NewImage: bson.M{
				"_id":   "p1",
				"price": 99.99,
				"stock": 100,
			},
			OldImage: bson.M{
				"_id":   "p1",
				"price": 89.99,
				"stock": 150,
			},
			Timestamp: time.Now(),
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded StreamRecord
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, original.TableName, decoded.TableName)
		assert.Equal(t, original.RecordType, decoded.RecordType)
		assert.NotNil(t, decoded.NewImage)
		assert.NotNil(t, decoded.OldImage)
	})
}

func TestWatcherCreation(t *testing.T) {
	t.Run("new watcher with correct configuration", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", true)

		assert.NotNil(t, watcher)
		assert.Equal(t, "conduit", watcher.database)
		assert.Equal(t, "users", watcher.table)
		assert.True(t, watcher.oldImage)
	})

	t.Run("watcher without old image", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "orders", false)

		assert.NotNil(t, watcher)
		assert.False(t, watcher.oldImage)
	})
}

func TestWatcherWatchContext(t *testing.T) {
	t.Run("watch respects context cancellation", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		records, errs, err := watcher.Watch(ctx)
		require.NoError(t, err)
		assert.NotNil(t, records)
		assert.NotNil(t, errs)

		// Channels should close quickly after context cancellation
		<-records
		<-errs
	})
}

func TestStreamRecordTimestamp(t *testing.T) {
	t.Run("timestamp is always set", func(t *testing.T) {
		before := time.Now()
		record := StreamRecord{
			TableName:  "test",
			RecordType: InsertRecord,
			Timestamp:  time.Now(),
		}
		after := time.Now()

		assert.True(t, !record.Timestamp.IsZero())
		assert.True(t, record.Timestamp.After(before) || record.Timestamp.Equal(before))
		assert.True(t, record.Timestamp.Before(after) || record.Timestamp.Equal(after))
	})
}

func TestStreamRecordJSONFormat(t *testing.T) {
	t.Run("JSON serialization matches expected format", func(t *testing.T) {
		record := StreamRecord{
			TableName:  "users",
			RecordType: InsertRecord,
			NewImage:   bson.M{"_id": "123", "name": "test"},
			Timestamp:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		}

		data, err := json.Marshal(record)
		require.NoError(t, err)

		var decoded map[string]interface{}
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "users", decoded["tableName"])
		assert.Equal(t, "INSERT", decoded["recordType"])
		assert.NotNil(t, decoded["newImage"])
	})
}
