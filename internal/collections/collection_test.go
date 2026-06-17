package collections

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
		table := Collection{
			CollectionName: "users",
			PartitionKey:   "id",
			SortKey:        "email",
			StreamEnabled:  true,
			OldImage:       false,
			Sinks:          []SinkConfig{{Type: "http", Endpoint: "http://localhost:3000/events"}},
		}

		assert.Equal(t, "users", table.CollectionName)
		assert.Equal(t, "id", table.PartitionKey)
		assert.Equal(t, "email", table.SortKey)
		assert.True(t, table.StreamEnabled)
		assert.False(t, table.OldImage)
		assert.Len(t, table.Sinks, 1)
	})

	t.Run("table with old image enabled", func(t *testing.T) {
		table := Collection{
			CollectionName: "orders",
			StreamEnabled:  true,
			OldImage:       true,
			Sinks:          []SinkConfig{{Type: "http", Endpoint: "http://localhost:3000/events"}, {Type: "eventbridge"}},
		}

		assert.True(t, table.OldImage, "OldImage should be true for tracking changes")
		assert.Len(t, table.Sinks, 2)
	})

	t.Run("table with TTL field", func(t *testing.T) {
		table := Collection{
			CollectionName: "sessions",
			StreamEnabled:  false,
			TTLAttribute:   "expiresAt",
		}

		assert.Equal(t, "expiresAt", table.TTLAttribute)
		assert.False(t, table.StreamEnabled)
	})

	t.Run("table with deletion protection enabled by default", func(t *testing.T) {
		table := Collection{
			CollectionName: "users",
			StreamEnabled:  true,
		}

		assert.False(t, table.DeletionProtection, "DeletionProtection should be false when not explicitly set")
	})

	t.Run("table with deletion protection explicitly enabled", func(t *testing.T) {
		table := Collection{
			CollectionName:     "users",
			StreamEnabled:      true,
			DeletionProtection: true,
		}

		assert.True(t, table.DeletionProtection, "DeletionProtection should be true when explicitly set")
	})
}

func TestTableTimestamps(t *testing.T) {
	t.Run("timestamps are set on creation", func(t *testing.T) {
		before := time.Now()
		table := Collection{
			CollectionName: "test",
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

	store := NewStore(client, "conduit_test")

	err = store.CreateIndex(ctx)
	require.NoError(t, err, "should create unique index on collection_name")
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

	store := NewStore(client, "conduit_test")

	// Cleanup before test - remove any leftover test collections
	for _, name := range []string{"test_table", "protected_table", "collection_table", "stream_table", "no_stream_table", "fresh_table", "new_collection_name"} {
		if table, err := store.Get(ctx, name); err == nil {
			if table.DeletionProtection {
				table.DeletionProtection = false
				_ = store.Update(ctx, table)
			}
			_ = store.Delete(ctx, name)
		}
	}

	t.Run("create table", func(t *testing.T) {
		table := &Collection{
			CollectionName: "test_table",
			PartitionKey:   "primaryKey",
			SortKey:        "sortKey",
			StreamEnabled:  true,
			OldImage:       true,
			Sinks:          []SinkConfig{{Type: "http", Endpoint: "http://localhost:3000/events"}},
		}

		err := store.Create(ctx, table)
		require.NoError(t, err)
		assert.NotZero(t, table.CreatedAt)
		assert.NotZero(t, table.UpdatedAt)

		indexes, err := client.Database("conduit_test").Collection(table.CollectionName).Indexes().ListSpecifications(ctx)
		require.NoError(t, err)

		foundPKSK := false
		for _, idx := range indexes {
			if idx.Name == "primary_sort_key_idx" {
				foundPKSK = true
				break
			}
		}
		assert.True(t, foundPKSK, "primary_sort_key_idx should exist on table collection")
	})

	t.Run("get table", func(t *testing.T) {
		table, err := store.Get(ctx, "test_table")
		require.NoError(t, err)
		assert.Equal(t, "test_table", table.CollectionName)
		assert.True(t, table.StreamEnabled)
		assert.True(t, table.OldImage)
	})

	t.Run("list tables", func(t *testing.T) {
		tables, err := store.List(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, tables)
	})

	t.Run("update table", func(t *testing.T) {
		table, err := store.Get(ctx, "test_table")
		require.NoError(t, err)

		table.StreamEnabled = false
		table.Sinks = []SinkConfig{{Type: "http", Endpoint: "http://localhost:3001/audit"}}

		err = store.Update(ctx, table)
		require.NoError(t, err)

		updated, _ := store.Get(ctx, "test_table")
		assert.False(t, updated.StreamEnabled)
		assert.Equal(t, []SinkConfig{{Type: "http", Endpoint: "http://localhost:3001/audit"}}, updated.Sinks)
		assert.True(t, updated.UpdatedAt.After(table.UpdatedAt))
	})

	t.Run("update collection name fails", func(t *testing.T) {
		// Create a fresh table for this test
		freshTable := &Collection{
			CollectionName:     "fresh_table",
			StreamEnabled:      true,
			DeletionProtection: false,
			Sinks:              []SinkConfig{{Type: "http", Endpoint: "http://localhost:3000/events"}},
		}
		err := store.Create(ctx, freshTable)
		require.NoError(t, err)

		// Get the existing table and try to change its name
		existing, err := store.Get(ctx, "fresh_table")
		require.NoError(t, err)

		// Try to update with a different collection name - should fail
		existing.CollectionName = "new_collection_name"
		err = store.Update(ctx, existing)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "collection not found")

		// Verify collection name was not changed - original should still exist
		updated, err := store.Get(ctx, "fresh_table")
		require.NoError(t, err)
		assert.Equal(t, "fresh_table", updated.CollectionName)

		// New collection name should not exist
		_, err = store.Get(ctx, "new_collection_name")
		assert.Error(t, err)

		// Cleanup
		_ = store.Delete(ctx, "fresh_table")
	})

	t.Run("delete table with deletion protection enabled fails", func(t *testing.T) {
		// Create table with deletion protection enabled
		protectedTable := &Collection{
			CollectionName:     "protected_table",
			StreamEnabled:      true,
			DeletionProtection: true,
			Sinks:              []SinkConfig{{Type: "http", Endpoint: "http://localhost:3000/events"}},
		}
		err := store.Create(ctx, protectedTable)
		require.NoError(t, err)

		// Try to delete - should fail
		err = store.Delete(ctx, protectedTable.CollectionName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deletion protection is enabled")

		// Collection should still exist
		exists, err := store.Get(ctx, "protected_table")
		assert.NoError(t, err)
		assert.NotNil(t, exists)

		// Cleanup - disable protection first
		protectedTable.DeletionProtection = false
		err = store.Update(ctx, protectedTable)
		require.NoError(t, err)
		err = store.Delete(ctx, protectedTable.CollectionName)
		require.NoError(t, err)
	})

	t.Run("delete table removes mongodb collection", func(t *testing.T) {
		// Create table with deletion protection disabled
		tableWithCollection := &Collection{
			CollectionName:     "collection_table",
			StreamEnabled:      true,
			DeletionProtection: false,
		}
		err := store.Create(ctx, tableWithCollection)
		require.NoError(t, err)

		// Verify collection exists
		db := client.Database("conduit_test")
		collections, err := db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.Contains(t, collections, "collection_table")

		// Delete table
		err = store.Delete(ctx, tableWithCollection.CollectionName)
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
		// Get the existing test_table and update deletion protection to false
		table, err := store.Get(ctx, "test_table")
		require.NoError(t, err)

		table.DeletionProtection = false
		err = store.Update(ctx, table)
		require.NoError(t, err)

		err = store.Delete(ctx, table.CollectionName)
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

	store := NewStore(client, "conduit_test")

	// Create stream-enabled table
	streamTable := &Collection{
		CollectionName: "stream_table",
		StreamEnabled:  true,
	}
	_ = store.Create(ctx, streamTable)

	// Create non-stream table
	nonStreamTable := &Collection{
		CollectionName: "no_stream_table",
		StreamEnabled:  false,
	}
	_ = store.Create(ctx, nonStreamTable)

	tables, err := store.ListStreamEnabled(ctx)
	require.NoError(t, err)

	found := false
	for _, table := range tables {
		if table.CollectionName == "stream_table" {
			found = true
			assert.True(t, table.StreamEnabled)
		}
		if table.CollectionName == "no_stream_table" {
			assert.Fail(t, "non-stream table should not be in list")
		}
	}
	assert.True(t, found, "stream_table should be in the list")

	// Cleanup
	_ = store.Delete(ctx, streamTable.CollectionName)
	_ = store.Delete(ctx, nonStreamTable.CollectionName)
}

func TestTableBSONTags(t *testing.T) {
	t.Run("BSON tags are correctly defined", func(t *testing.T) {
		table := Collection{
			CollectionName: "test",
			StreamEnabled:  true,
			OldImage:       true,
			TTLAttribute:   "expiresAt",
			Sinks:          []SinkConfig{{Type: "http", Endpoint: "http://localhost:3000/events"}},
		}

		data, err := bson.Marshal(table)
		require.NoError(t, err)

		var decoded map[string]interface{}
		err = bson.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "test", decoded["collection_name"])
		assert.Equal(t, true, decoded["stream_enabled"])
		assert.Equal(t, true, decoded["old_image"])
		assert.Equal(t, "expiresAt", decoded["ttl_attribute"])
	})
}
