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

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(localMongoURI))
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
	t.Run("list all documents", func(t *testing.T) {
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

		documents, err := store.List(ctx, DocumentListOptions{})
		require.NoError(t, err)
		assert.Len(t, documents, 25)
	})

	t.Run("list applies limit", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		coll := store.client.Database(store.database).Collection(store.collection)
		for i := 0; i < 25; i++ {
			coll.InsertOne(ctx, bson.M{
				"_id":   string(rune('A' + i)),
				"name":  "Item " + string(rune('A'+i)),
				"value": i,
			})
		}

		documents, err := store.List(ctx, DocumentListOptions{Limit: 5})
		require.NoError(t, err)
		assert.Len(t, documents, 5)
	})

	t.Run("list applies skip", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		coll := store.client.Database(store.database).Collection(store.collection)
		for i := 0; i < 10; i++ {
			coll.InsertOne(ctx, bson.M{
				"_id":   string(rune('A' + i)),
				"name":  "Item " + string(rune('A'+i)),
				"value": i,
			})
		}

		documents, err := store.List(ctx, DocumentListOptions{Skip: 5})
		require.NoError(t, err)
		assert.Len(t, documents, 5)
		// Sorted by _id ascending, so skipping 5 returns the last 5 items.
		assert.Equal(t, "Item F", documents[0]["name"])
	})

	t.Run("list applies limit and skip together", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		coll := store.client.Database(store.database).Collection(store.collection)
		for i := 0; i < 10; i++ {
			coll.InsertOne(ctx, bson.M{
				"_id":   string(rune('A' + i)),
				"name":  "Item " + string(rune('A'+i)),
				"value": i,
			})
		}

		documents, err := store.List(ctx, DocumentListOptions{Limit: 3, Skip: 4})
		require.NoError(t, err)
		assert.Len(t, documents, 3)
		assert.Equal(t, "Item E", documents[0]["name"])
		assert.Equal(t, "Item G", documents[2]["name"])
	})

	t.Run("list empty collection returns empty array", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		documents, err := store.List(ctx, DocumentListOptions{})
		require.NoError(t, err)
		assert.NotNil(t, documents)
		assert.Empty(t, documents)
	})

	t.Run("list with limit and skip on empty collection returns empty array", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		documents, err := store.List(ctx, DocumentListOptions{Limit: 10, Skip: 5})
		require.NoError(t, err)
		assert.NotNil(t, documents)
		assert.Empty(t, documents)
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

	t.Run("get with non-objectid id fails immediately", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// A non-ObjectID id is rejected without touching the database.
		_, err := store.Get(ctx, "custom-id-123")
		require.ErrorIs(t, err, ErrDocumentNotFound)
	})

	t.Run("get not found returns error", func(t *testing.T) {
		store, cleanup := setupTestDocument(t)
		defer cleanup()

		ctx := context.Background()

		// A well-formed ObjectID that does not exist in the collection.
		_, err := store.Get(ctx, primitive.NewObjectID().Hex())
		require.ErrorIs(t, err, ErrDocumentNotFound)
	})
}
