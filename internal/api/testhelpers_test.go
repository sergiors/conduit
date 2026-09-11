package api

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"testing"
	"time"

	"conduit/internal/apikey"
	"conduit/internal/collections"
	"conduit/internal/mongo"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// localMongoURI mirrors the collections test helper: the compose MongoDB runs
// as a single-node replica set (rs0) advertising its internal hostname, so
// directConnection=true is required to reach it from outside the compose
// network.
const localMongoURI = "mongodb://localhost:27017/?directConnection=true"

var discardLogger = log.New(io.Discard, "", 0)

// testToken is the plaintext API key created for integration tests. It is set
// by newMongoTestServer and consumed by doRequest.
var testToken string

// doRequest performs an authenticated request against the server's router using
// the test API key token captured at server construction.
func doRequest(t *testing.T, server *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	server.Router().ServeHTTP(rec, req)
	return rec
}

// newMongoTestServer connects to MongoDB, drops any leftover state, and returns
// a fully wired Server plus the underlying collections.Manager and mongo client.
// It skips the test if MongoDB is not available or running in -short mode.
func newMongoTestServer(t *testing.T, database string) (*Server, *collections.Manager, *mongo.Client, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	client, err := mongo.NewClient(ctx, mongo.Config{
		URI:      localMongoURI,
		Database: database,
	}, discardLogger)
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	t.Cleanup(func() { client.Close(context.Background()) })

	// Drop any leftover state from a previous run so the test is idempotent.
	require.NoError(t, client.Client.Database(database).Drop(ctx))

	manager := collections.NewManager(client.Client, database, discardLogger)
	require.NoError(t, manager.CreateIndex(ctx))

	apiKeys := apikey.NewManager(client.Client, database, discardLogger)
	require.NoError(t, apiKeys.CreateIndex(ctx))

	// Create a real persisted API key; doRequest authenticates with its
	// plaintext secret.
	_, secret, err := apiKeys.Create(ctx, "test")
	require.NoError(t, err)
	testToken = secret

	server := New(Dependencies{
		Collections: manager,
		MongoClient: client,
		APIKeys:     apiKeys,
	})

	return server, manager, client, ctx
}
