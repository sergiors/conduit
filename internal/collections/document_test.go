package collections

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func setupTestDocument(t *testing.T) (*Document, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	require.NoError(t, err)

	// Use a unique test database and collection per test
	db := "conduit_test_" + time.Now().Format("20060102150405")
	collection := "test_items_" + t.Name()

	store := NewDocument(client, db, collection)

	// Create the collection
	err = client.Database(db).CreateCollection(ctx, collection)
	require.NoError(t, err)

	cleanup := func() {
		client.Database(db).Drop(ctx)
		client.Disconnect(ctx)
	}

	return store, cleanup
}

func TestItem_List(t *testing.T) {
	t.Run("list items with pagination", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// Insert test data
		coll := store.client.Database(store.database).Collection(store.collection)
		for i := 0; i < 25; i++ {
			coll.InsertOne(ctx, bson.M{
				"_id":   string(rune('A' + i)),
				"name":  "Item " + string(rune('A'+i)),
				"value": i,
			})
		}

		// Test page 1
		result, err := store.List(ctx, DocumentQuery{
			Page:  1,
			Limit: 10,
			Sort:  bson.M{"value": 1},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(10), result.Limit)
		assert.Equal(t, int64(1), result.Page)
		assert.Equal(t, int64(25), result.Total)
		assert.Equal(t, int64(3), result.TotalPages)
		assert.Len(t, result.Documents, 10)

		// Test page 2
		result, err = store.List(ctx, DocumentQuery{
			Page:  2,
			Limit: 10,
			Sort:  bson.M{"value": 1},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(2), result.Page)
		assert.Len(t, result.Documents, 10)

		// Test page 3 (last page)
		result, err = store.List(ctx, DocumentQuery{
			Page:  3,
			Limit: 10,
			Sort:  bson.M{"value": 1},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(3), result.Page)
		assert.Len(t, result.Documents, 5)
	})

	t.Run("list with filter", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// Insert test data
		coll := store.client.Database(store.database).Collection(store.collection)
		coll.InsertOne(ctx, bson.M{"_id": "1", "status": "active", "name": "Active 1"})
		coll.InsertOne(ctx, bson.M{"_id": "2", "status": "active", "name": "Active 2"})
		coll.InsertOne(ctx, bson.M{"_id": "3", "status": "inactive", "name": "Inactive 1"})

		result, err := store.List(ctx, DocumentQuery{
			Filter: bson.M{"status": "active"},
			Sort:   bson.M{"name": 1},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(2), result.Total)
		assert.Len(t, result.Documents, 2)
	})

	t.Run("list empty collection returns empty array", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		result, err := store.List(ctx, DocumentQuery{})
		require.NoError(t, err)
		assert.Equal(t, int64(0), result.Total)
		assert.NotNil(t, result.Documents)
		assert.Empty(t, result.Documents)
	})

	t.Run("default pagination values", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		result, err := store.List(ctx, DocumentQuery{})
		require.NoError(t, err)
		assert.Equal(t, int64(1), result.Page)
		assert.Equal(t, int64(20), result.Limit)
	})

	t.Run("limit max is 100", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		result, err := store.List(ctx, DocumentQuery{
			Limit: 500,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(100), result.Limit)
	})
}

func TestItem_Get(t *testing.T) {
	t.Run("get item by object id", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// Insert test data
		coll := store.client.Database(store.database).Collection(store.collection)
		insertResult, _ := coll.InsertOne(ctx, bson.M{
			"name":  "Test Item",
			"value": 123,
		})
		objectID := insertResult.InsertedID.(primitive.ObjectID).Hex()

		item, err := store.Get(ctx, objectID)
		require.NoError(t, err)
		assert.Equal(t, "Test Item", item["name"])
		assert.Equal(t, int32(123), item["value"])
	})

	t.Run("get item by string id", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// Insert with custom string ID
		coll := store.client.Database(store.database).Collection(store.collection)
		coll.InsertOne(ctx, bson.M{
			"_id":   "custom-id-123",
			"name":  "Custom Item",
			"value": 456,
		})

		item, err := store.Get(ctx, "custom-id-123")
		require.NoError(t, err)
		assert.Equal(t, "Custom Item", item["name"])
	})

	t.Run("get not found returns error", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		_, err := store.Get(ctx, "nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}


func TestItem_Create(t *testing.T) {
	t.Run("create item with auto timestamps", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		result, err := store.Create(ctx, bson.M{
			"pk":   "USER#123",
			"name": "Test User",
		})
		require.NoError(t, err)

		assert.NotNil(t, result["_id"])
	})

	t.Run("create item returns inserted document with id", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		result, err := store.Create(ctx, bson.M{
			"pk":   "USER#789",
			"name": "New User",
		})
		require.NoError(t, err)

		assert.NotNil(t, result["_id"])
		assert.Equal(t, "USER#789", result["pk"])
		assert.Equal(t, "New User", result["name"])
	})

	t.Run("create item with pk and sk preserves fields", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		result, err := store.Create(ctx, bson.M{
			"pk":   "USER#999",
			"sk":   "EMAIL#primary",
			"name": "Primary Email",
		})
		require.NoError(t, err)

		assert.NotNil(t, result["_id"])
		assert.Equal(t, "USER#999", result["pk"])
		assert.Equal(t, "EMAIL#primary", result["sk"])
	})
}

func TestItem_Update(t *testing.T) {
	t.Run("update existing item", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// Create item first
		createResult, _ := store.Create(ctx, bson.M{
			"pk":    "USER#123",
			"name":  "Original",
			"value": 100,
		})
		var id string
		switch v := createResult["_id"].(type) {
		case primitive.ObjectID:
			id = v.Hex()
		case string:
			id = v
		}

		// Update
		result, err := store.Update(ctx, id, bson.M{
			"name":  "Updated",
			"value": 200,
		})
		require.NoError(t, err)

		assert.Equal(t, "Updated", result["name"])
		assert.Equal(t, 200, result["value"])
	})

	t.Run("update removes _id from data", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		createResult, _ := store.Create(ctx, bson.M{
			"pk": "USER#123",
		})
		var id string
		switch v := createResult["_id"].(type) {
		case primitive.ObjectID:
			id = v.Hex()
		case string:
			id = v
		}

		result, err := store.Update(ctx, id, bson.M{
			"_id":  "MALICIOUS_ID",
			"name": "Hacker",
		})
		require.NoError(t, err)

		assert.NotEqual(t, "MALICIOUS_ID", result["_id"])
	})

	t.Run("update not found returns error", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		_, err := store.Update(ctx, "nonexistent", bson.M{
			"name": "Updated",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestItem_Delete(t *testing.T) {
	t.Run("delete existing item", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// Create item first
		createResult, _ := store.Create(ctx, bson.M{
			"pk": "USER#123",
		})
		var id string
		switch v := createResult["_id"].(type) {
		case primitive.ObjectID:
			id = v.Hex()
		case string:
			id = v
		}

		// Delete
		err := store.Delete(ctx, id)
		require.NoError(t, err)

		// Verify deleted
		_, err = store.Get(ctx, id)
		require.Error(t, err)
	})

	t.Run("delete not found returns error", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		err := store.Delete(ctx, "nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}