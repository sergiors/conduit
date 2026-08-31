package collections

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// localMongoURI is the URI for tests running on the host. The compose MongoDB
// runs as a single-node replica set (rs0) that advertises its internal hostname
// (mongo:27017) via the seed-list redirect; without directConnection=true the
// driver would try to reconnect to that unresolvable host from outside the
// compose network.
const localMongoURI = "mongodb://localhost:27017/?directConnection=true"

// newTestManager connects to MongoDB and returns a Manager instance for
// integration tests. It skips the test if MongoDB is not available.
func newTestManager(t *testing.T) (*Manager, *mongo.Client, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(localMongoURI))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	t.Cleanup(func() { client.Disconnect(context.Background()) })

	return NewManager(client, "conduit_test"), client, ctx
}
