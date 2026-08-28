package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		cfg := Config{
			URI:      "mongodb://localhost:27017",
			Database: "conduit",
		}

		assert.Equal(t, "mongodb://localhost:27017", cfg.URI)
		assert.Equal(t, "conduit", cfg.Database)
	})

	t.Run("custom configuration", func(t *testing.T) {
		cfg := Config{
			URI:      "mongodb://custom:27017",
			Database: "mydb",
		}

		assert.Equal(t, "mongodb://custom:27017", cfg.URI)
		assert.Equal(t, "mydb", cfg.Database)
	})
}

func TestClientCreation(t *testing.T) {
	t.Run("client creation with invalid URI fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Use invalid URI to ensure failure
		_, err := NewClient(ctx, Config{
			URI:      "mongodb://invalid-host:27017",
			Database: "test",
		})

		assert.Error(t, err, "should fail with invalid host")
	})
}

// Integration tests
func TestClientIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	t.Run("connect to MongoDB", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client, err := NewClient(ctx, Config{
			URI:      "mongodb://localhost:27017/?replicaSet=rs0",
			Database: "conduit",
		})
		if err != nil {
			t.Skipf("MongoDB not available: %v", err)
		}
		defer client.Close(ctx)

		assert.NotNil(t, client.Client)
		assert.Equal(t, "conduit", client.Database())
	})

	t.Run("get collection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client, err := NewClient(ctx, Config{
			URI:      "mongodb://localhost:27017/?replicaSet=rs0",
			Database: "conduit",
		})
		if err != nil {
			t.Skipf("MongoDB not available: %v", err)
		}
		defer client.Close(ctx)

		collection := client.Collection("users")
		assert.NotNil(t, collection)
		assert.Equal(t, "users", collection.Name())
	})
}

func TestClientClose(t *testing.T) {
	t.Run("close client gracefully", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a client that won't connect (invalid URI)
		mongoClient, _ := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://invalid:27017"))

		client := &Client{
			Client:   mongoClient,
			database: "test",
		}

		// Should not panic, just close the client
		err := client.Close(ctx)
		assert.NoError(t, err)
	})
}

func TestClientMethods(t *testing.T) {
	t.Run("database returns correct name", func(t *testing.T) {
		client := &Client{
			Client:   &mongo.Client{},
			database: "mydb",
		}

		assert.Equal(t, "mydb", client.Database())
	})

	t.Run("collection returns mongo collection", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mongoClient, _ := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://invalid"))
		client := &Client{
			Client:   mongoClient,
			database: "testdb",
		}

		coll := client.Collection("users")
		assert.NotNil(t, coll)
		assert.Equal(t, "users", coll.Name())
	})
}
