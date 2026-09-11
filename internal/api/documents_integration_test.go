//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// newDocumentTestServer connects to MongoDB and returns a fully wired Server
// plus the underlying collections.Manager and mongo client. It skips the test
// if MongoDB is not available.
func newDocumentTestServer(t *testing.T) (*Server, *collections.Manager, *mongo.Client, context.Context) {
	return newMongoTestServer(t, "conduit_test_docs")
}

// TestDocumentEndpointsRegisteredCollectionOnly verifies that both document
// endpoints only allow access to collections registered in Conduit's
// config.collections, using collections.Manager.Get as the source of truth.
func TestDocumentEndpointsRegisteredCollectionOnly(t *testing.T) {
	server, manager, client, ctx := newDocumentTestServer(t)

	// Create a managed collection and seed a document in its physical collection.
	// Document IDs are ObjectIDs per the collections.Document contract.
	managed := &collections.Collection{CollectionName: "managed_docs"}
	require.NoError(t, manager.Create(ctx, managed))
	docID := primitive.NewObjectID()
	_, err := client.Collection("managed_docs").InsertOne(ctx, bson.M{"_id": docID, "name": "hello"})
	require.NoError(t, err)

	// Seed an UNREGISTERED physical collection that must never be readable.
	_, err = client.Collection("unregistered_docs").InsertOne(ctx, bson.M{"_id": "secret", "name": "leak"})
	require.NoError(t, err)

	t.Run("registered collection list works", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/managed_docs/documents")
		require.Equal(t, http.StatusOK, rec.Code)
		var docs []bson.M
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &docs))
		require.Len(t, docs, 1)
		assert.Equal(t, "hello", docs[0]["name"])
	})

	t.Run("registered collection single doc works", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/managed_docs/documents/"+docID.Hex())
		require.Equal(t, http.StatusOK, rec.Code)
		var doc bson.M
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
		assert.Equal(t, "hello", doc["name"])
	})

	t.Run("existing physical collection not registered returns 404", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/unregistered_docs/documents")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})

	t.Run("nonexistent collection returns 404", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/does_not_exist/documents")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})

	t.Run("single-document endpoint enforces same rule for unregistered collection", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/unregistered_docs/documents/doc-1")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})

	t.Run("single-document endpoint enforces same rule for nonexistent collection", func(t *testing.T) {
		rec := doRequest(t, server, http.MethodGet, "/api/collections/does_not_exist/documents/doc-1")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body ErrorResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "collection_not_found", body.Error.Code)
	})
}
