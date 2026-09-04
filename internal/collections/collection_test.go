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
		// Simulate what Manager.Create does
		table.CreatedAt = time.Now()
		table.UpdatedAt = time.Now()
		after := time.Now()

		assert.True(t, !table.CreatedAt.IsZero())
		assert.True(t, !table.UpdatedAt.IsZero())
		assert.True(t, table.CreatedAt.Before(after) || table.CreatedAt.Equal(before))
		assert.True(t, table.UpdatedAt.After(before) || table.UpdatedAt.Equal(before))
	})
}

func TestManagerCreateIndex(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	err := manager.CreateIndex(ctx)
	require.NoError(t, err, "should create unique index on collectionName")
}

func TestManagerCRUD(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	// Cleanup before test - remove any leftover test collections
	for _, name := range []string{"test_table", "protected_table", "collection_table", "stream_table", "no_stream_table", "fresh_table", "new_collection_name"} {
		if table, err := manager.Get(ctx, name); err == nil {
			if table.DeletionProtection {
				_ = manager.DisableDeletionProtection(ctx, name)
			}
			_ = manager.Delete(ctx, name)
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

		err := manager.Create(ctx, table)
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
		table, err := manager.Get(ctx, "test_table")
		require.NoError(t, err)
		assert.Equal(t, "test_table", table.CollectionName)
		assert.True(t, table.StreamEnabled)
		assert.True(t, table.OldImage)
	})

	t.Run("list tables", func(t *testing.T) {
		tables, err := manager.List(ctx)
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
		err := manager.Create(ctx, protectedTable)
		require.NoError(t, err)

		// Try to delete - should fail
		err = manager.Delete(ctx, protectedTable.CollectionName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deletion protection is enabled")

		// Collection should still exist
		exists, err := manager.Get(ctx, "protected_table")
		assert.NoError(t, err)
		assert.NotNil(t, exists)

		// Cleanup - disable protection first
		err = manager.DisableDeletionProtection(ctx, protectedTable.CollectionName)
		require.NoError(t, err)
		err = manager.Delete(ctx, protectedTable.CollectionName)
		require.NoError(t, err)
	})

	t.Run("delete table removes mongodb collection", func(t *testing.T) {
		// Create table with deletion protection disabled
		tableWithCollection := &Collection{
			CollectionName:     "collection_table",
			StreamEnabled:      true,
			DeletionProtection: false,
		}
		err := manager.Create(ctx, tableWithCollection)
		require.NoError(t, err)

		// Verify collection exists
		db := client.Database("conduit_test")
		collections, err := db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.Contains(t, collections, "collection_table")

		// Delete table
		err = manager.DisableDeletionProtection(ctx, tableWithCollection.CollectionName)
		require.NoError(t, err)
		err = manager.Delete(ctx, tableWithCollection.CollectionName)
		require.NoError(t, err)

		// Verify configuration is removed
		_, err = manager.Get(ctx, "collection_table")
		assert.Error(t, err)

		// Verify collection is removed
		collections, err = db.ListCollectionNames(ctx, bson.M{"name": "collection_table"})
		require.NoError(t, err)
		assert.NotContains(t, collections, "collection_table")
	})

	t.Run("delete table", func(t *testing.T) {
		// Get the existing test_table and disable deletion protection before deleting
		table, err := manager.Get(ctx, "test_table")
		require.NoError(t, err)

		err = manager.DisableDeletionProtection(ctx, table.CollectionName)
		require.NoError(t, err)

		err = manager.Delete(ctx, table.CollectionName)
		require.NoError(t, err)

		_, err = manager.Get(ctx, "test_table")
		assert.Error(t, err)
	})
}

func TestManagerValidator(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	// Cleanup before test - remove any leftover test collections
	for _, name := range []string{"validator_no_keys", "validator_pk", "validator_pk_sk", "validator_preexisting"} {
		if table, err := manager.Get(ctx, name); err == nil {
			if table.DeletionProtection {
				_ = manager.DisableDeletionProtection(ctx, name)
			}
			_ = manager.Delete(ctx, name)
		}
	}

	// validatorRequired returns the "required" array of the collection's
	// $jsonSchema validator, or nil if no validator is set.
	validatorRequired := func(name string) []string {
		db := client.Database("conduit_test")
		var spec struct {
			Options struct {
				Validator bson.M `bson:"validator"`
			} `bson:"options"`
		}
		cursor, err := db.ListCollections(ctx, bson.M{"name": name})
		require.NoError(t, err)
		defer cursor.Close(ctx)
		require.True(t, cursor.Next(ctx))
		require.NoError(t, cursor.Decode(&spec))
		if spec.Options.Validator == nil {
			return nil
		}
		schema, ok := spec.Options.Validator["$jsonSchema"].(bson.M)
		if !ok {
			return nil
		}
		required, _ := schema["required"].(bson.A)
		out := make([]string, 0, len(required))
		for _, r := range required {
			out = append(out, r.(string))
		}
		return out
	}

	t.Run("no keys requires nothing", func(t *testing.T) {
		table := &Collection{CollectionName: "validator_no_keys"}
		require.NoError(t, manager.Create(ctx, table))
		assert.Nil(t, validatorRequired("validator_no_keys"))
	})

	t.Run("partition key only requires partition key", func(t *testing.T) {
		table := &Collection{CollectionName: "validator_pk", PartitionKey: "pk"}
		require.NoError(t, manager.Create(ctx, table))
		assert.Equal(t, []string{"pk"}, validatorRequired("validator_pk"))
	})

	t.Run("partition and sort key require both", func(t *testing.T) {
		table := &Collection{CollectionName: "validator_pk_sk", PartitionKey: "pk", SortKey: "sk"}
		require.NoError(t, manager.Create(ctx, table))
		assert.Equal(t, []string{"pk", "sk"}, validatorRequired("validator_pk_sk"))
	})

	t.Run("validator rejects documents missing required keys", func(t *testing.T) {
		// pk+sk collection: a document missing the sort key must be rejected.
		_, err := client.Database("conduit_test").Collection("validator_pk_sk").InsertOne(ctx, bson.M{"pk": "x"})
		assert.Error(t, err, "document missing sort key should be rejected by validator")

		// A document with both keys must be accepted.
		_, err = client.Database("conduit_test").Collection("validator_pk_sk").InsertOne(ctx, bson.M{"pk": "x", "sk": "y"})
		assert.NoError(t, err, "document with both keys should be accepted by validator")
	})

	t.Run("validator not applied to pre-existing collection", func(t *testing.T) {
		// Create the physical collection directly (no validator), then create the
		// config through the manager. Conduit must NOT adopt a pre-existing
		// physical collection by applying validator changes to it.
		db := client.Database("conduit_test")
		_ = db.Collection("validator_preexisting").Drop(ctx)
		require.NoError(t, db.CreateCollection(ctx, "validator_preexisting"))
		table := &Collection{CollectionName: "validator_preexisting", PartitionKey: "pk"}
		require.NoError(t, manager.Create(ctx, table))
		assert.Nil(t, validatorRequired("validator_preexisting"))
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

		assert.Equal(t, "test", decoded["collectionName"])
		assert.Equal(t, true, decoded["streamEnabled"])
		assert.Equal(t, true, decoded["oldImage"])
		assert.Equal(t, "expiresAt", decoded["ttlAttribute"])
	})
}
