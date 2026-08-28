package mongo

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Client wraps the MongoDB client with application-specific methods
type Client struct {
	*mongo.Client
	database string
}

// Config holds MongoDB connection configuration
type Config struct {
	URI      string
	Database string
	Timeout  time.Duration
}

// NewClient creates a new MongoDB client.
//
// It connects and then waits until MongoDB is actually ready to serve change
// streams: a writable PRIMARY must exist and the replica-set-mode client must
// be able to reach it. This prevents NotPrimaryOrSecondary failures when MongoDB
// is still electing a PRIMARY after a restart.
//
// The application never creates or modifies replica sets; it only waits for
// readiness. Replica set topology is managed externally (by MongoDB
// administrators or operators).
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.URI == "" {
		return nil, fmt.Errorf("MONGODB_URI is required")
	}
	if cfg.Database == "" {
		return nil, fmt.Errorf("MONGODB_DATABASE is required")
	}

	clientOpts := options.Client().ApplyURI(cfg.URI)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("connect to mongo: %w", err)
	}

	c := &Client{
		Client:   client,
		database: cfg.Database,
	}

	// Wait until MongoDB is actually usable before returning. This is what
	// prevents NotPrimaryOrSecondary failures when MongoDB is still electing a
	// PRIMARY after a restart.
	if err := waitForWritablePrimary(ctx, client); err != nil {
		client.Disconnect(context.Background())
		return nil, fmt.Errorf("wait for writable primary: %w", err)
	}
	if err := waitForClientReady(ctx, client); err != nil {
		client.Disconnect(context.Background())
		return nil, fmt.Errorf("wait for mongo client: %w", err)
	}

	return c, nil
}

// Database returns the database name
func (c *Client) Database() string {
	return c.database
}

// Collection gets a collection from the configured database
func (c *Client) Collection(name string) *mongo.Collection {
	return c.Client.Database(c.database).Collection(name)
}

// Close closes the MongoDB connection
func (c *Client) Close(ctx context.Context) error {
	return c.Client.Disconnect(ctx)
}

// waitForWritablePrimary polls the hello command until the node reports itself
// as the writable PRIMARY. Transient errors while the node is recovering or
// electing a PRIMARY are retried until the context is done.
func waitForWritablePrimary(ctx context.Context, client *mongo.Client) error {
	helloCmd := bson.D{{Key: "hello", Value: 1}}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		var hello struct {
			IsWritablePrimary bool    `bson:"isWritablePrimary"`
			OK                float64 `bson:"ok"`
		}
		err := client.Database("admin").RunCommand(ctx, helloCmd).Decode(&hello)

		if err == nil && hello.OK == 1 && hello.IsWritablePrimary {
			log.Println("MongoDB node is writable PRIMARY")
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for writable primary: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// waitForClientReady pings the client until it can reach a server, ensuring the
// replica-set-mode client has discovered the PRIMARY. Transient errors are
// retried until the context is done.
func waitForClientReady(ctx context.Context, client *mongo.Client) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := client.Ping(ctx, nil); err == nil {
			log.Println("MongoDB client ready")
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for mongo client: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
