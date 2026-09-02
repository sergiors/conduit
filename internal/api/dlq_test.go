package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

// newDLQTestServer connects to MongoDB and returns a fully wired Server plus
// the underlying collections.Manager and mongo client. It skips the test if
// MongoDB is not available.
func newDLQTestServer(t *testing.T) (*Server, *collections.Manager, *mongo.Client, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	client, err := mongo.NewClient(ctx, mongo.Config{
		URI:      localMongoURI,
		Database: "conduit_test_dlq_api",
	})
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	t.Cleanup(func() { client.Close(context.Background()) })

	require.NoError(t, client.Client.Database("conduit_test_dlq_api").Drop(ctx))

	manager := collections.NewManager(client.Client, "conduit_test_dlq_api")
	require.NoError(t, manager.CreateIndex(ctx))

	server := New(Dependencies{
		Collections: manager,
		MongoClient: client,
		APIKey:      "test-key",
	})

	return server, manager, client, ctx
}

// TestDLQEndpointsRegisteredCollectionOnly verifies that both DLQ endpoints
// only allow access to collections registered in Conduit's config.collections,
// and that a DLQ entry from another collection cannot be retrieved through a
// requested collection.
func TestDLQEndpointsRegisteredCollectionOnly(t *testing.T) {
	server, manager, mongoClient, ctx := newDLQTestServer(t)

	// Create two managed collections and seed DLQ entries for each.
	users := &collections.Collection{CollectionName: "managed_dlq_users"}
	require.NoError(t, manager.Create(ctx, users))
	orders := &collections.Collection{CollectionName: "managed_dlq_orders"}
	require.NoError(t, manager.Create(ctx, orders))

	require.NoError(t, manager.CreateDLQEntry(ctx, collections.DLQEntry{
		CollectionName: "managed_dlq_users",
		EventData:      []byte(`{"tableName":"managed_dlq_users"}`),
		FailedAt:       time.Now(),
		DedupKey:       "managed_dlq_users:event-1",
	}))
	require.NoError(t, manager.CreateDLQEntry(ctx, collections.DLQEntry{
		CollectionName: "managed_dlq_orders",
		EventData:      []byte(`{"tableName":"managed_dlq_orders"}`),
		FailedAt:       time.Now(),
		DedupKey:       "managed_dlq_orders:event-1",
	}))

	// Seed an UNREGISTERED physical collection that must never be readable.
	_, err := mongoClient.Collection("unregistered_dlq").InsertOne(ctx, bson.M{"_id": "secret"})
	require.NoError(t, err)

	t.Run("registered collection list works", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/managed_dlq_users/dlq")
		require.Equal(t, http.StatusOK, rec.Code)
		var entries []collections.DLQEntry
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entries))
		require.Len(t, entries, 1)
		assert.Equal(t, "managed_dlq_users", entries[0].CollectionName)
	})

	t.Run("registered collection single entry works", func(t *testing.T) {
		entries, err := manager.ListDLQEntries(ctx, "managed_dlq_users", collections.DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, entries, 1)

		rec := doRequest(t, server, http.MethodGet, "/api/collections/managed_dlq_users/dlq/"+entries[0].ID.Hex())
		require.Equal(t, http.StatusOK, rec.Code)
		var entry collections.DLQEntry
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entry))
		assert.Equal(t, "managed_dlq_users", entry.CollectionName)
	})

	t.Run("entry from another collection cannot be retrieved through requested collection", func(t *testing.T) {
		ordersEntries, err := manager.ListDLQEntries(ctx, "managed_dlq_orders", collections.DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, ordersEntries, 1)

		// Request the orders entry through the users collection: must 404.
		rec := doRequest(t, server, http.MethodGet, "/api/collections/managed_dlq_users/dlq/"+ordersEntries[0].ID.Hex())
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "dlq_entry_not_found", body.Error.Code)
	})

	t.Run("existing physical collection not registered returns 404", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/unregistered_dlq/dlq")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})

	t.Run("nonexistent collection returns 404", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/does_not_exist/dlq")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})

	t.Run("single-entry endpoint enforces same rule for unregistered collection", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/unregistered_dlq/dlq/abc")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})
}

func TestParseDLQListOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(query string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/dlq?"+query, nil)
		return c
	}

	t.Run("defaults when no params", func(t *testing.T) {
		opts, err := parseDLQListOptions(newCtx(""))
		require.NoError(t, err)
		assert.Equal(t, int64(defaultDLQLimit), opts.Limit)
		assert.Equal(t, int64(0), opts.Skip)
	})

	t.Run("accepts explicit limit and skip", func(t *testing.T) {
		opts, err := parseDLQListOptions(newCtx("limit=50&skip=10"))
		require.NoError(t, err)
		assert.Equal(t, int64(50), opts.Limit)
		assert.Equal(t, int64(10), opts.Skip)
	})

	t.Run("caps limit above max", func(t *testing.T) {
		opts, err := parseDLQListOptions(newCtx("limit=5000"))
		require.NoError(t, err)
		assert.Equal(t, int64(maxDLQLimit), opts.Limit)
	})

	t.Run("rejects malformed limit", func(t *testing.T) {
		_, err := parseDLQListOptions(newCtx("limit=abc"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})

	t.Run("rejects zero limit", func(t *testing.T) {
		_, err := parseDLQListOptions(newCtx("limit=0"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})

	t.Run("rejects negative skip", func(t *testing.T) {
		_, err := parseDLQListOptions(newCtx("skip=-1"))
		require.Error(t, err)
		assert.IsType(t, &badRequestError{}, err)
	})
}
