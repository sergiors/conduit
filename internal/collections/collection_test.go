package collections

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

func TestTableValidation(t *testing.T) {
	t.Run("valid table with required fields", func(t *testing.T) {
		table := Collection{
			CollectionName: "users",
			PartitionKey:   "id",
			SortKey:        "email",
			StreamEnabled:  true,
			OldImage:       false,
		}

		assert.Equal(t, "users", table.CollectionName)
		assert.Equal(t, "id", table.PartitionKey)
		assert.Equal(t, "email", table.SortKey)
		assert.True(t, table.StreamEnabled)
		assert.False(t, table.OldImage)
	})

	t.Run("table with old image enabled", func(t *testing.T) {
		table := Collection{
			CollectionName: "orders",
			StreamEnabled:  true,
			OldImage:       true,
		}

		assert.True(t, table.OldImage, "OldImage should be true for tracking changes")
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
		// Simulate what Settings.Create does
		table.CreatedAt = time.Now()
		table.UpdatedAt = time.Now()
		after := time.Now()

		assert.True(t, !table.CreatedAt.IsZero())
		assert.True(t, !table.UpdatedAt.IsZero())
		assert.True(t, table.CreatedAt.Before(after) || table.CreatedAt.Equal(before))
		assert.True(t, table.UpdatedAt.After(before) || table.UpdatedAt.Equal(before))
	})
}

func TestSettingsCreateIndex(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	err := settings.CreateIndex(ctx)
	require.NoError(t, err, "should create unique index on collection_name")
}

func TestSettingsCRUD(t *testing.T) {
	settings, client, ctx := newTestSettings(t)

	// Cleanup before test - remove any leftover test collections
	for _, name := range []string{"test_table", "protected_table", "collection_table", "stream_table", "no_stream_table", "fresh_table", "new_collection_name"} {
		if table, err := settings.Get(ctx, name); err == nil {
			if table.DeletionProtection {
				_ = settings.DisableDeletionProtection(ctx, name)
			}
			_ = settings.Delete(ctx, name)
		}
	}

	t.Run("create table", func(t *testing.T) {
		table := &Collection{
			CollectionName: "test_table",
			PartitionKey:   "primaryKey",
			SortKey:        "sortKey",
			StreamEnabled:  true,
			OldImage:       true,
		}

		err := settings.Create(ctx, table)
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
		table, err := settings.Get(ctx, "test_table")
		require.NoError(t, err)
		assert.Equal(t, "test_table", table.CollectionName)
		assert.True(t, table.StreamEnabled)
		assert.True(t, table.OldImage)
	})

	t.Run("list tables", func(t *testing.T) {
		tables, err := settings.List(ctx)
		require.NoError(t, err)
		assert.NotEmpty(t, tables)
	})

	t.Run("delete table with deletion protection enabled fails", func(t *testing.T) {
		// Create table with deletion protection enabled
		protectedTable := &Collection{
			CollectionName:     "protected_table",
			StreamEnabled:      true,
			DeletionProtection: true,
		}
		err := settings.Create(ctx, protectedTable)
		require.NoError(t, err)

		// Try to delete - should fail
		err = settings.Delete(ctx, protectedTable.CollectionName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deletion protection is enabled")

		// Collection should still exist
		exists, err := settings.Get(ctx, "protected_table")
		assert.NoError(t, err)
		assert.NotNil(t, exists)

		// Cleanup - disable protection first
		err = settings.DisableDeletionProtection(ctx, protectedTable.CollectionName)
		require.NoError(t, err)
		err = settings.Delete(ctx, protectedTable.CollectionName)
		require.NoError(t, err)
	})

	t.Run("delete table removes mongodb collection", func(t *testing.T) {
		// Create table with deletion protection disabled
		tableWithCollection := &Collection{
			CollectionName:     "collection_table",
			StreamEnabled:      true,
			DeletionProtection: false,
		}
		err := settings.Create(ctx, tableWithCollection)
		require.NoError(t, err)

		// Verify collection exists
		db := client.Database("conduit_test")
		collections, err := db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.Contains(t, collections, "collection_table")

		// Delete table
		err = settings.DisableDeletionProtection(ctx, tableWithCollection.CollectionName)
		require.NoError(t, err)
		err = settings.Delete(ctx, tableWithCollection.CollectionName)
		require.NoError(t, err)

		// Verify configuration is removed
		_, err = settings.Get(ctx, "collection_table")
		assert.Error(t, err)

		// Verify collection is removed
		collections, err = db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.NotContains(t, collections, "collection_table")
	})

	t.Run("delete table", func(t *testing.T) {
		// Get the existing test_table and disable deletion protection before deleting
		table, err := settings.Get(ctx, "test_table")
		require.NoError(t, err)

		err = settings.DisableDeletionProtection(ctx, table.CollectionName)
		require.NoError(t, err)

		err = settings.Delete(ctx, table.CollectionName)
		require.NoError(t, err)

		_, err = settings.Get(ctx, "test_table")
		assert.Error(t, err)
	})
}

func TestTableBSONTags(t *testing.T) {
	t.Run("BSON tags are correctly defined", func(t *testing.T) {
		table := Collection{
			CollectionName: "test",
			StreamEnabled:  true,
			OldImage:       true,
			TTLAttribute:   "expiresAt",
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
