package mongo

import (
	"context"
	"fmt"
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

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() Config {
	return Config{
		URI:      "mongodb://localhost:27017",
		Database: "relay",
		Timeout:  10 * time.Second,
	}
}

// NewClient creates a new MongoDB client
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	clientOpts := options.Client().ApplyURI(cfg.URI)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("connect to mongo: %w", err)
	}

	// Verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("ping mongo: %w", err)
	}

	return &Client{
		Client:   client,
		database: cfg.Database,
	}, nil
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

// CreateTTLIndex creates a TTL index on a collection
// Uses expireAfterSeconds=0 as per AGENTS.md spec
func (c *Client) CreateTTLIndex(ctx context.Context, collection, field string) error {
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: field, Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	}
	_, err := c.Collection(collection).Indexes().CreateOne(ctx, indexModel)
	return err
}

// EnableTableStreams configures a collection for change streams
// Uses fullDocument=updateLookup and optionally fullDocumentBeforeChange
func (c *Client) EnableTableStreams(ctx context.Context, collection string, oldImage bool) error {
	// MongoDB change streams are enabled by default for replica sets
	// This method validates the collection exists and logs the configuration
	opts := options.ChangeStream()
	opts.SetFullDocument(options.UpdateLookup)
	if oldImage {
		opts.SetFullDocumentBeforeChange(options.Required)
	}

	// Validate collection can be watched
	coll := c.Collection(collection)
	cursor, err := coll.Watch(ctx, mongo.Pipeline{}, opts)
	if err != nil {
		return err
	}
	return cursor.Close(ctx)
}

// EnsureReplicaSet initializes a replica set if not already configured
// This is required for change streams to work
func (c *Client) EnsureReplicaSet(ctx context.Context) error {
	// Check if already a replica set member
	rsStatus, err := c.Client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).DecodeBytes()
	if err == nil {
		// Already initialized, check state
		ok, _ := rsStatus.Lookup("ok").AsInt64OK()
		if ok == 1 {
			return nil // Replica set already running
		}
	}

	// Not initialized, try to initiate
	// Single-node replica set for development
	config := bson.D{
		{Key: "_id", Value: "rs0"},
		{Key: "members", Value: bson.A{
			bson.D{
				{Key: "_id", Value: 0},
				{Key: "host", Value: "localhost:27017"},
			},
		}},
	}

	cmd := bson.D{{Key: "replSetInitiate", Value: config}}
	result := c.Client.Database("admin").RunCommand(ctx, cmd)

	// Check result - "already initialized" is ok
	var res bson.M
	if err := result.Decode(&res); err != nil {
		// Check if it's "already initialized" error
		if err.Error() == "AlreadyInitialized" || err.Error() == "replSetInitiate already initiated" {
			return nil
		}
		// For other errors, log but don't fail - might be running in replica set already
		return fmt.Errorf("initiate replica set: %w", err)
	}

	return nil
}
