package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		cfg := Config{
			URI:      "mongodb://localhost:27017",
			Database: "conduit",
			Timeout:  10 * time.Second,
		}

		assert.Equal(t, "mongodb://localhost:27017", cfg.URI)
		assert.Equal(t, "conduit", cfg.Database)
		assert.Equal(t, 10*time.Second, cfg.Timeout)
	})

	t.Run("custom configuration", func(t *testing.T) {
		cfg := Config{
			URI:      "mongodb://custom:27017",
			Database: "mydb",
			Timeout:  30 * time.Second,
		}

		assert.Equal(t, "mongodb://custom:27017", cfg.URI)
		assert.Equal(t, "mydb", cfg.Database)
		assert.Equal(t, 30*time.Second, cfg.Timeout)
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
			Timeout:  2 * time.Second,
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
			URI:      "mongodb://localhost:27017",
			Database: "conduit",
			Timeout:  10 * time.Second,
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
			URI:      "mongodb://localhost:27017",
			Database: "conduit",
			Timeout:  10 * time.Second,
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

func TestClientCreateTTLIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{
			URI:      "mongodb://localhost:27017",
			Database: "conduit",
			Timeout:  10 * time.Second,
		})
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	defer client.Close(ctx)

	t.Run("create TTL index on collection", func(t *testing.T) {
		collection := "test_ttl_collection"
		field := "expiresAt"

		// Cleanup before test
		_ = client.Collection(collection).Drop(ctx)

		err := client.CreateTTLIndex(ctx, collection, field)
		require.NoError(t, err)

		// Verify index was created
		cursor, err := client.Collection(collection).Indexes().List(ctx)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var indexes []bson.M
		err = cursor.All(ctx, &indexes)
		require.NoError(t, err)

		found := false
		for _, idx := range indexes {
			if idx["name"] == field+"_1" {
				found = true
				assert.Equal(t, int32(0), idx["expireAfterSeconds"])
			}
		}
		assert.True(t, found, "TTL index should be created")

		// Cleanup
		_ = client.Collection(collection).Drop(ctx)
	})

	t.Run("TTL index uses expireAfterSeconds=0", func(t *testing.T) {
		collection := "test_ttl_zero"

		_ = client.Collection(collection).Drop(ctx)

		err := client.CreateTTLIndex(ctx, collection, "createdAt")
		require.NoError(t, err)

		cursor, err := client.Collection(collection).Indexes().List(ctx)
		require.NoError(t, err)
		defer cursor.Close(ctx)

		var indexes []bson.M
		err = cursor.All(ctx, &indexes)
		require.NoError(t, err)

		for _, idx := range indexes {
			if idx["name"] != "_id_" {
				expireVal := idx["expireAfterSeconds"]
				assert.Equal(t, int32(0), expireVal, "TTL should use expireAfterSeconds=0")
			}
		}

		// Cleanup
		_ = client.Collection(collection).Drop(ctx)
	})
}

func TestClientEnableStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{
			URI:      "mongodb://localhost:27017",
			Database: "conduit",
			Timeout:  10 * time.Second,
		})
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	defer client.Close(ctx)

	t.Run("enable streams without old image", func(t *testing.T) {
		collection := "test_stream_collection"
		_ = client.Collection(collection).Drop(ctx)

		// Create collection first
		_, err := client.Collection(collection).InsertOne(ctx, bson.M{"_id": "1"})
		require.NoError(t, err)

		err = client.EnableStreams(ctx, collection, false)
		require.NoError(t, err)

		// Cleanup
		_ = client.Collection(collection).Drop(ctx)
	})

	t.Run("enable streams with old image", func(t *testing.T) {
		collection := "test_stream_old"
		_ = client.Collection(collection).Drop(ctx)

		_, err := client.Collection(collection).InsertOne(ctx, bson.M{"_id": "1"})
		require.NoError(t, err)

		err = client.EnableStreams(ctx, collection, true)
		require.NoError(t, err)

		// Cleanup
		_ = client.Collection(collection).Drop(ctx)
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
