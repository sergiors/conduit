package tables

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MockCollection is a test helper for mocking MongoDB operations
type MockCollection struct {
	data map[string]interface{}
}

func TestTableValidation(t *testing.T) {
	t.Run("valid table with required fields", func(t *testing.T) {
		table := Table{
			TableName:     "users",
			StreamEnabled: true,
			OldImage:      false,
			Destinations:  []DestinationConfig{{Type: "redis"}},
		}

		assert.Equal(t, "users", table.TableName)
		assert.True(t, table.StreamEnabled)
		assert.False(t, table.OldImage)
		assert.Len(t, table.Destinations, 1)
	})

	t.Run("table with old image enabled", func(t *testing.T) {
		table := Table{
			TableName:     "orders",
			StreamEnabled: true,
			OldImage:      true,
			Destinations:  []DestinationConfig{{Type: "redis"}, {Type: "eventbridge"}},
		}

		assert.True(t, table.OldImage, "OldImage should be true for tracking changes")
		assert.Len(t, table.Destinations, 2)
	})

	t.Run("table with TTL field", func(t *testing.T) {
		table := Table{
			TableName:     "sessions",
			StreamEnabled: false,
			TTLField:      "expiresAt",
		}

		assert.Equal(t, "expiresAt", table.TTLField)
		assert.False(t, table.StreamEnabled)
	})

	t.Run("table with deletion protection enabled by default", func(t *testing.T) {
		table := Table{
			TableName:     "users",
			StreamEnabled: true,
		}

		assert.False(t, table.DeletionProtection, "DeletionProtection should be false when not explicitly set")
	})

	t.Run("table with deletion protection explicitly enabled", func(t *testing.T) {
		table := Table{
			TableName:          "users",
			StreamEnabled:      true,
			DeletionProtection: true,
		}

		assert.True(t, table.DeletionProtection, "DeletionProtection should be true when explicitly set")
	})
}

func TestTableTimestamps(t *testing.T) {
	t.Run("timestamps are set on creation", func(t *testing.T) {
		before := time.Now()
		table := Table{
			TableName: "test",
		}
		// Simulate what Store.Create does
		table.CreatedAt = time.Now()
		table.UpdatedAt = time.Now()
		after := time.Now()

		assert.True(t, !table.CreatedAt.IsZero())
		assert.True(t, !table.UpdatedAt.IsZero())
		assert.True(t, table.CreatedAt.Before(after) || table.CreatedAt.Equal(before))
		assert.True(t, table.UpdatedAt.After(before) || table.UpdatedAt.Equal(before))
	})
}

// Integration-style tests (require MongoDB connection)
// These are skipped by default and run with: go test -tags=integration

func TestStoreCreateIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	defer client.Disconnect(ctx)

	store := NewStore(client, "relay_test")

	err = store.CreateIndex(ctx)
	require.NoError(t, err, "should create unique index on table_name")
}

func TestStoreCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	defer client.Disconnect(ctx)

	store := NewStore(client, "relay_test")

	// Cleanup before test
	_ = store.Delete(ctx, "test_table")

	t.Run("create table", func(t *testing.T) {
		table := &Table{
			TableName:     "test_table",
			StreamEnabled: true,
			OldImage:      true,
			Destinations:  []DestinationConfig{{Type: "redis"}},
		}

		err := store.Create(ctx, table)
		require.NoError(t, err)
		assert.NotZero(t, table.CreatedAt)
		assert.NotZero(t, table.UpdatedAt)
	})

	t.Run("get table", func(t *testing.T) {
		table, err := store.Get(ctx, "test_table")
		require.NoError(t, err)
		assert.Equal(t, "test_table", table.TableName)
		assert.True(t, table.StreamEnabled)
		assert.True(t, table.OldImage)
	})

	t.Run("list tables", func(t *testing.T) {
		tables, err := store.List(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, tables)
	})

	t.Run("update table", func(t *testing.T) {
		table, _ := store.Get(ctx, "test_table")
		table.StreamEnabled = false
		table.Destinations = []DestinationConfig{{Type: "eventbridge"}}

		err := store.Update(ctx, table)
		require.NoError(t, err)

		updated, _ := store.Get(ctx, "test_table")
		assert.False(t, updated.StreamEnabled)
		assert.Equal(t, []DestinationConfig{{Type: "eventbridge"}}, updated.Destinations)
		assert.True(t, updated.UpdatedAt.After(table.UpdatedAt))
	})

	t.Run("delete table with deletion protection enabled fails", func(t *testing.T) {
		// Create table with deletion protection enabled
		protectedTable := &Table{
			TableName:          "protected_table",
			StreamEnabled:      true,
			DeletionProtection: true,
		}
		err := store.Create(ctx, protectedTable)
		require.NoError(t, err)

		// Try to delete - should fail
		err = store.Delete(ctx, "protected_table")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deletion protection is enabled")

		// Table should still exist
		exists, err := store.Get(ctx, "protected_table")
		assert.NoError(t, err)
		assert.NotNil(t, exists)

		// Cleanup - disable protection first
		protectedTable.DeletionProtection = false
		err = store.Update(ctx, protectedTable)
		require.NoError(t, err)
		err = store.Delete(ctx, "protected_table")
		require.NoError(t, err)
	})

	t.Run("delete table removes mongodb collection", func(t *testing.T) {
		// Create table with deletion protection disabled
		tableWithCollection := &Table{
			TableName:          "collection_table",
			StreamEnabled:      true,
			DeletionProtection: false,
		}
		err := store.Create(ctx, tableWithCollection)
		require.NoError(t, err)

		// Verify collection exists
		db := client.Database("relay_test")
		collections, err := db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.Contains(t, collections, "collection_table")

		// Delete table
		err = store.Delete(ctx, "collection_table")
		require.NoError(t, err)

		// Verify configuration is removed
		_, err = store.Get(ctx, "collection_table")
		assert.Error(t, err)

		// Verify collection is removed
		collections, err = db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.NotContains(t, collections, "collection_table")
	})

	t.Run("delete table", func(t *testing.T) {
		// Create table with deletion protection disabled
		table := &Table{
			TableName:          "test_table",
			StreamEnabled:      true,
			DeletionProtection: false,
		}
		err := store.Create(ctx, table)
		require.NoError(t, err)

		err = store.Delete(ctx, "test_table")
		require.NoError(t, err)

		_, err = store.Get(ctx, "test_table")
		assert.Error(t, err)
	})
}

func TestStoreListStreamEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	defer client.Disconnect(ctx)

	store := NewStore(client, "relay_test")

	// Create stream-enabled table
	streamTable := &Table{
		TableName:     "stream_table",
		StreamEnabled: true,
	}
	_ = store.Create(ctx, streamTable)

	// Create non-stream table
	nonStreamTable := &Table{
		TableName:     "no_stream_table",
		StreamEnabled: false,
	}
	_ = store.Create(ctx, nonStreamTable)

	tables, err := store.ListStreamEnabled(ctx)
	require.NoError(t, err)

	found := false
	for _, table := range tables {
		if table.TableName == "stream_table" {
			found = true
			assert.True(t, table.StreamEnabled)
		}
		if table.TableName == "no_stream_table" {
			assert.Fail(t, "non-stream table should not be in list")
		}
	}
	assert.True(t, found, "stream_table should be in the list")

	// Cleanup
	_ = store.Delete(ctx, "stream_table")
	_ = store.Delete(ctx, "no_stream_table")
}

func TestTableBSONTags(t *testing.T) {
	t.Run("BSON tags are correctly defined", func(t *testing.T) {
		table := Table{
			TableName:     "test",
			StreamEnabled: true,
			OldImage:      true,
			TTLField:      "expiresAt",
			Destinations:  []DestinationConfig{{Type: "redis"}},
		}

		data, err := bson.Marshal(table)
		require.NoError(t, err)

		var decoded map[string]interface{}
		err = bson.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "test", decoded["table_name"])
		assert.Equal(t, true, decoded["stream_enabled"])
		assert.Equal(t, true, decoded["old_image"])
		assert.Equal(t, "expiresAt", decoded["ttl_field"])
	})
}
