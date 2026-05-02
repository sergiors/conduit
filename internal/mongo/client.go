package mongo

import (
	"context"
	"fmt"
	"log"
	"time"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Client wraps the MongoDB client with application-specific methods
type Client struct {
	*mongo.Client
	database string
	uri      string
}

// Config holds MongoDB connection configuration
type Config struct {
	URI      string
	Database string
	Timeout  time.Duration
}

// NewClient creates a new MongoDB client
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

	// Verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("ping mongo: %w", err)
	}

	return &Client{
		Client:   client,
		database: cfg.Database,
		uri:      cfg.URI,
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

// InitializeReplicaSet initializes a MongoDB replica set if not already initialized
// This is required for change streams to work
func (c *Client) InitializeReplicaSet(ctx context.Context) error {
	// Check if replica set is already initialized
	rsStatusCmd := bson.D{{Key: "replSetGetStatus", Value: 1}}
	result := c.Client.Database("admin").RunCommand(ctx, rsStatusCmd)

	var rsStatus bson.M
	if err := result.Decode(&rsStatus); err == nil {
		// Replica set already initialized
		log.Println("Replica set already initialized")
		return nil
	}

	// Check if error is "not initialized" vs other errors
	// If not initialized, proceed with initialization
	log.Println("Initializing replica set...")

	// Extract host from URI (similar to redis.ParseURL)
	host := strings.TrimPrefix(c.uri, "mongodb://")
	host = strings.TrimPrefix(host, "mongodb+srv://")
	if idx := strings.Index(host, "@"); idx != -1 {
		host = host[idx+1:]
	}
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}
	if idx := strings.Index(host, "?"); idx != -1 {
		host = host[:idx]
	}

	// Initialize replica set
	initCmd := bson.D{
		{Key: "replSetInitiate", Value: 1},
		{Key: "conf", Value: bson.D{
			{Key: "_id", Value: "rs0"},
			{Key: "members", Value: bson.A{
				bson.D{
					{Key: "_id", Value: 0},
					{Key: "host", Value: host},
				},
			}},
		}},
	}

	res := c.Client.Database("admin").RunCommand(ctx, initCmd)
	var initResult bson.M
	if err := res.Decode(&initResult); err != nil {
		return fmt.Errorf("initiate replica set: %w", err)
	}

	log.Println("Replica set initialized successfully")
	return nil
}
