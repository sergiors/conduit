package collections

import (
	"context"
	"errors"
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
				_ = store.SetDeletionProtection(ctx, name, false)
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
		err = store.SetDeletionProtection(ctx, protectedTable.CollectionName, false)
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
		// Get the existing test_table and disable deletion protection before deleting
		table, err := store.Get(ctx, "test_table")
		require.NoError(t, err)

		err = store.SetDeletionProtection(ctx, table.CollectionName, false)
		require.NoError(t, err)

		err = store.Delete(ctx, table.CollectionName)
		require.NoError(t, err)

		_, err = store.Get(ctx, "test_table")
		assert.Error(t, err)
	})
}

func TestStoreSetDeletionProtection(t *testing.T) {
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

	// Cleanup any leftover from previous runs
	if tbl, err := store.Get(ctx, "protection_toggle_table"); err == nil {
		if tbl.DeletionProtection {
			_ = store.SetDeletionProtection(ctx, "protection_toggle_table", false)
		}
		_ = store.Delete(ctx, "protection_toggle_table")
	}

	table := &Collection{
		CollectionName:     "protection_toggle_table",
		StreamEnabled:      true,
		DeletionProtection: false,
	}
	require.NoError(t, store.Create(ctx, table))

	t.Run("enable protection", func(t *testing.T) {
		require.NoError(t, store.SetDeletionProtection(ctx, "protection_toggle_table", true))
		got, err := store.Get(ctx, "protection_toggle_table")
		require.NoError(t, err)
		assert.True(t, got.DeletionProtection, "protection should be enabled")
	})

	t.Run("enable is idempotent", func(t *testing.T) {
		require.NoError(t, store.SetDeletionProtection(ctx, "protection_toggle_table", true))
		got, _ := store.Get(ctx, "protection_toggle_table")
		assert.True(t, got.DeletionProtection)
	})

	t.Run("delete blocked while protected", func(t *testing.T) {
		err := store.Delete(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deletion protection")
	})

	t.Run("disable protection", func(t *testing.T) {
		require.NoError(t, store.SetDeletionProtection(ctx, "protection_toggle_table", false))
		got, _ := store.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection, "protection should be disabled")
	})

	t.Run("disable is idempotent", func(t *testing.T) {
		require.NoError(t, store.SetDeletionProtection(ctx, "protection_toggle_table", false))
		got, _ := store.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection)
	})

	t.Run("unknown collection returns not found", func(t *testing.T) {
		err := store.SetDeletionProtection(ctx, "does_not_exist_table", true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "collection not found")
		assert.True(t, errors.Is(err, ErrCollectionNotFound), "should match ErrCollectionNotFound")
	})

	t.Run("delete protected returns ErrDeletionProtectionEnabled", func(t *testing.T) {
		require.NoError(t, store.SetDeletionProtection(ctx, "protection_toggle_table", true))
		err := store.Delete(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrDeletionProtectionEnabled), "should match ErrDeletionProtectionEnabled")
		// Restore to unprotected so the cleanup below can delete it.
		require.NoError(t, store.SetDeletionProtection(ctx, "protection_toggle_table", false))
	})

	// Cleanup
	require.NoError(t, store.Delete(ctx, "protection_toggle_table"))
}

func TestStoreStreamAndTTL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	defer client.Disconnect(ctx)

	store := NewStore(client, "conduit_test")

	// Cleanup leftovers
	if _, err := store.Get(ctx, "stream_ttl_table"); err == nil {
		_, _ = store.DisableTTL(ctx, "stream_ttl_table")
		_ = store.SetDeletionProtection(ctx, "stream_ttl_table", false)
		_ = store.Delete(ctx, "stream_ttl_table")
	}

	table := &Collection{
		CollectionName: "stream_ttl_table",
		StreamEnabled:  false,
	}
	require.NoError(t, store.Create(ctx, table))

	t.Run("enable stream with old_image", func(t *testing.T) {
		require.NoError(t, store.SetStream(ctx, "stream_ttl_table", true))
		got, err := store.Get(ctx, "stream_ttl_table")
		require.NoError(t, err)
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
	})

	t.Run("re-enable stream with same old_image is immutable", func(t *testing.T) {
		err := store.SetStream(ctx, "stream_ttl_table", true)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrOldImageImmutable), "should match ErrOldImageImmutable")
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
	})

	t.Run("change old_image after enabled is immutable", func(t *testing.T) {
		err := store.SetStream(ctx, "stream_ttl_table", false)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrOldImageImmutable), "should match ErrOldImageImmutable")
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.True(t, got.OldImage, "old_image should remain unchanged")
	})

	t.Run("disable stream resets both and allows redefinition", func(t *testing.T) {
		require.NoError(t, store.DisableStream(ctx, "stream_ttl_table"))
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.False(t, got.StreamEnabled)
		assert.False(t, got.OldImage)

		require.NoError(t, store.SetStream(ctx, "stream_ttl_table", false))
		got, _ = store.Get(ctx, "stream_ttl_table")
		assert.True(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
	})

	t.Run("disable stream is idempotent", func(t *testing.T) {
		require.NoError(t, store.DisableStream(ctx, "stream_ttl_table"))
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.False(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
	})

	t.Run("enable ttl sets attribute", func(t *testing.T) {
		require.NoError(t, store.SetTTL(ctx, "stream_ttl_table", "expiresAt"))
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.Equal(t, "expiresAt", got.TTLAttribute)
	})

	t.Run("re-enable ttl with same attribute is immutable", func(t *testing.T) {
		err := store.SetTTL(ctx, "stream_ttl_table", "expiresAt")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrTTLAttributeImmutable), "should match ErrTTLAttributeImmutable")
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.Equal(t, "expiresAt", got.TTLAttribute)
	})

	t.Run("enable ttl different attribute is immutable", func(t *testing.T) {
		err := store.SetTTL(ctx, "stream_ttl_table", "ttl")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrTTLAttributeImmutable), "should match ErrTTLAttributeImmutable")
		// Attribute is unchanged
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.Equal(t, "expiresAt", got.TTLAttribute)
	})

	t.Run("enable ttl empty attribute is validation error", func(t *testing.T) {
		err := store.SetTTL(ctx, "stream_ttl_table", "")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrValidation), "should match ErrValidation")
	})

	t.Run("disable ttl clears attribute and returns previous", func(t *testing.T) {
		previous, err := store.DisableTTL(ctx, "stream_ttl_table")
		require.NoError(t, err)
		assert.Equal(t, "expiresAt", previous)
		got, _ := store.Get(ctx, "stream_ttl_table")
		assert.Equal(t, "", got.TTLAttribute)
	})

	t.Run("disable ttl idempotent", func(t *testing.T) {
		previous, err := store.DisableTTL(ctx, "stream_ttl_table")
		require.NoError(t, err)
		assert.Equal(t, "", previous)
	})

	t.Run("stream and ttl on unknown collection return not found", func(t *testing.T) {
		assert.True(t, errors.Is(store.SetStream(ctx, "does_not_exist", true), ErrCollectionNotFound))
		assert.True(t, errors.Is(store.DisableStream(ctx, "does_not_exist"), ErrCollectionNotFound))
		assert.True(t, errors.Is(store.SetTTL(ctx, "does_not_exist", "expiresAt"), ErrCollectionNotFound))
		_, err := store.DisableTTL(ctx, "does_not_exist")
		assert.True(t, errors.Is(err, ErrCollectionNotFound))
	})

	// Cleanup
	require.NoError(t, store.Delete(ctx, "stream_ttl_table"))
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
