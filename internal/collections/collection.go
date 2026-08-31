package collections

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Collection struct {
	ID                 string    `bson:"_id,omitempty" json:"_id,omitempty"`
	CollectionName     string    `bson:"collection_name,omitempty" json:"collection_name,omitempty"`
	PartitionKey       string    `bson:"partition_key,omitempty" json:"partition_key,omitempty"`
	SortKey            string    `bson:"sort_key,omitempty" json:"sort_key,omitempty"`
	StreamEnabled      bool      `bson:"stream_enabled" json:"stream_enabled"`
	OldImage           bool      `bson:"old_image" json:"old_image"`
	TTLAttribute       string    `bson:"ttl_attribute,omitempty" json:"ttl_attribute,omitempty"`
	DeletionProtection bool      `bson:"deletion_protection" json:"deletion_protection"`
	CreatedAt          time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time `bson:"updated_at" json:"updated_at"`
}

// Settings manages collection configurations in MongoDB
type Settings struct {
	client     *mongo.Client
	database   string
	collection *mongo.Collection
	sinks      *mongo.Collection

	// OnPublish is an optional hook invoked after any successful configuration
	// mutation (collection created or deleted, stream/TTL enabled or disabled,
	// sink created or deleted), with the affected collection's name. It exists so
	// callers can fan out config-change notifications without coupling this
	// package to their infrastructure: cmd/api assigns it a method value that
	// publishes to Redis pub/sub. nil (the zero value) means "no notification".
	// Invocations are best-effort: an error is logged, never returned — a
	// committed mutation must not be reported as failed because notification
	// bookkeeping hiccuped.
	OnPublish func(ctx context.Context, collectionName string) error

	// OnPurge is an optional hook invoked by Delete ONLY, after the MongoDB-side
	// deletion (drop + sinks + config document) has fully succeeded, to remove
	// out-of-band CDC artifacts (e.g. Redis resume token, retry queue, dead-letter
	// queue). Same contract as OnPublish: nil-safe, best-effort, name-scoped,
	// idempotent. Delete runs it on a DETACHED bounded context (5s) so an
	// abandoned request cannot cancel a committed deletion's cleanup.
	OnPurge func(ctx context.Context, collectionName string) error
}

// NewSettings creates a new collection settings manager
func NewSettings(client *mongo.Client, database string) *Settings {
	return &Settings{
		client:     client,
		database:   database,
		collection: client.Database(database).Collection("config.collections"),
		sinks:      client.Database(database).Collection("config.sinks"),
	}
}

// notifyPublish fires OnPublish best-effort after a successful configuration
// mutation. It is nil-safe and never returns an error: a committed mutation
// must not be reported as failed because notification bookkeeping hiccuped.
// The hook runs on the caller's context (the same request ctx the handler used
// today); a failure is logged and swallowed.
func (s *Settings) notifyPublish(ctx context.Context, name string) {
	if s.OnPublish == nil {
		return
	}
	if err := s.OnPublish(ctx, name); err != nil {
		log.Printf("failed to publish config change for %s: %v", name, err)
	}
}

// purgeState runs OnPurge best-effort on a DETACHED bounded context (5s) so an
// abandoned request cannot cancel a committed deletion's cleanup. It is
// nil-safe and never returns an error; a failure is logged and swallowed. The
// hook is idempotent, so a failed purge can be retried by re-running it.
func (s *Settings) purgeState(ctx context.Context, name string) {
	if s.OnPurge == nil {
		return
	}
	purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.OnPurge(purgeCtx, name); err != nil {
		log.Printf("failed to purge CDC state after deleting collection %s: %v", name, err)
	}
}

// CreateIndex creates the indexes required by the settings.
func (s *Settings) CreateIndex(ctx context.Context) error {
	collectionIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "collection_name", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := s.collection.Indexes().CreateOne(ctx, collectionIndex); err != nil {
		return err
	}

	sinkIndex := mongo.IndexModel{
		Keys: bson.D{{Key: "collection_id", Value: 1}},
	}
	_, err := s.sinks.Indexes().CreateOne(ctx, sinkIndex)
	return err
}

// createCollection ensures the physical MongoDB collection exists.
func (s *Settings) createCollection(ctx context.Context, collection *Collection) error {
	db := s.client.Database(s.database)
	collectionName := collection.CollectionName

	// Check if collection already exists
	collections, err := db.ListCollectionNames(ctx, bson.M{"name": collectionName})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}

	if len(collections) > 0 {
		// Collection already exists, just ensure the key index
		if err := s.ensureKeyIndex(ctx, collectionName, collection.PartitionKey, collection.SortKey); err != nil {
			return fmt.Errorf("ensure key index: %w", err)
		}
		return nil
	}

	// Create the collection with pre- and post-image support enabled. This is a
	// permanent capability of every managed collection, enabled exactly once at
	// creation. It is independent of the old_image runtime flag: MongoDB is
	// always capable of producing pre-images, and Conduit decides at runtime
	// whether to request and forward them. EnableStream additionally ensures
	// the capability for collections created outside this path.
	createOpts := options.CreateCollection().SetChangeStreamPreAndPostImages(bson.M{"enabled": true})
	err = db.CreateCollection(ctx, collectionName, createOpts)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// Insert a dummy document to ensure the collection is not empty
	// (empty collections can cause issues with change streams)
	_, err = db.Collection(collectionName).InsertOne(ctx, bson.M{
		"_id":          "init",
		"_placeholder": true,
		"_created_at":  time.Now(),
	})
	if err != nil {
		return fmt.Errorf("insert placeholder: %w", err)
	}

	// Remove the placeholder document
	_, err = db.Collection(collectionName).DeleteOne(ctx, bson.M{"_id": "init"})
	if err != nil {
		return fmt.Errorf("delete placeholder: %w", err)
	}

	if err := s.ensureKeyIndex(ctx, collectionName, collection.PartitionKey, collection.SortKey); err != nil {
		return fmt.Errorf("ensure key index: %w", err)
	}

	return nil
}

func (s *Settings) ensureKeyIndex(ctx context.Context, collectionName, primaryKey, sortKey string) error {
	if primaryKey == "" {
		return nil
	}
	if sortKey != "" && sortKey == primaryKey {
		return fmt.Errorf("sort key cannot be the same as primary key")
	}

	keys := bson.D{{Key: primaryKey, Value: 1}}
	indexName := "primary_key_idx"
	if sortKey != "" {
		keys = append(keys, bson.E{Key: sortKey, Value: 1})
		indexName = "primary_sort_key_idx"
	}

	indexModel := mongo.IndexModel{
		Keys:    keys,
		Options: options.Index().SetName(indexName).SetUnique(true),
	}

	_, err := s.client.Database(s.database).Collection(collectionName).Indexes().CreateOne(ctx, indexModel)
	return err
}

// Create inserts a new collection configuration and creates the MongoDB collection.
// On success, fires OnPublish (best-effort).
func (s *Settings) Create(ctx context.Context, collection *Collection) error {
	name := collection.CollectionName
	if name == "" {
		return NewValidationError("collection name is required")
	}

	if collection.SortKey != "" && collection.PartitionKey == "" {
		return NewValidationError("partition_key is required when sort_key is defined")
	}

	if collection.PartitionKey != "" && collection.PartitionKey == collection.SortKey {
		return NewValidationError("sort_key cannot be the same as primary_key")
	}

	// Deletion protection is mandatory on create.
	collection.DeletionProtection = true

	now := time.Now()
	collection.CreatedAt = now
	collection.UpdatedAt = now

	// Create the actual MongoDB collection for CDC monitoring
	if err := s.createCollection(ctx, collection); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	result, err := s.collection.InsertOne(ctx, collection)
	if err != nil {
		return err
	}
	// Set the ID on the collection so it can be returned to the client
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		collection.ID = objectID.Hex()
	}
	s.notifyPublish(ctx, name)
	return nil
}

// Get retrieves a collection by name
func (s *Settings) Get(ctx context.Context, name string) (*Collection, error) {
	var collection Collection
	err := s.collection.FindOne(ctx, bson.M{"collection_name": name}).Decode(&collection)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCollectionNotFound
		}
		return nil, err
	}

	return &collection, nil
}

// List returns all collection configurations
func (s *Settings) List(ctx context.Context) ([]Collection, error) {
	cursor, err := s.collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var collections []Collection
	if err := cursor.All(ctx, &collections); err != nil {
		return nil, err
	}
	return collections, nil
}

// Delete removes a collection configuration and its MongoDB collection by
// collection name. Returns ErrCollectionNotFound if the collection does not
// exist, or ErrDeletionProtectionEnabled if deletion protection is on.
//
// Operation order (kept exact and unswappable):
//  1. drop the physical MongoDB collection,
//  2. delete the associated sinks,
//  3. delete the config.collections document,
//  4. purge out-of-band CDC state (OnPurge, detached bounded context),
//  5. fire OnPublish (best-effort).
//
// Settings.Delete owns the side-effect fan-out via OnPurge then OnPublish, both
// best-effort. Purge runs BEFORE publish: the pub/sub tick may cause the worker
// to disable the collection, so better that the Redis keys are already gone.
// Neither hook is required (nil is a no-op) and neither failure is returned —
// a committed deletion must not be reported as failed because bookkeeping
// hiccuped. Cleanup of the Redis CDC state (resume token, retry queue,
// dead-letter queue) is performed by Delete itself via OnPurge; the hook is
// idempotent, and a failed purge can be retried by deleting the collection
// again or by invoking OnPurge manually. Settings and its hooks are the single
// owner of deletion cleanup — the worker manager never purges on disable,
// because disabled != deleted.
//
// Known minor edge: a delete immediately followed by a recreate of the same
// name inside one worker sync interval may resume the recreated watcher from
// the pre-delete resume token. This is harmless: the token positions at an old
// oplog point on the SAME collection name — MongoDB either accepts it (no
// error) or invalidates it and the stream restarts — never stalling. The
// purge fires synchronously before the config-change pub/sub tick, so this
// window is narrow.
func (s *Settings) Delete(ctx context.Context, name string) error {
	// Get collection to check deletion protection
	collection, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	// Check deletion protection
	if collection.DeletionProtection {
		return ErrDeletionProtectionEnabled
	}

	// Drop the MongoDB collection
	db := s.client.Database(s.database)
	if err := db.Collection(collection.CollectionName).Drop(ctx); err != nil {
		return fmt.Errorf("drop collection: %w", err)
	}

	// Delete associated sinks
	if err := s.deleteSinksByCollectionID(ctx, collection.ID); err != nil {
		return err
	}

	// Delete the configuration
	_, err = s.collection.DeleteOne(ctx, bson.M{"collection_name": name})
	if err != nil {
		return err
	}

	// The MongoDB-side deletion has fully succeeded. From here on every side
	// effect is best-effort bookkeeping: its failure must never turn a
	// committed deletion into an error. Purge first, then publish.
	s.purgeState(ctx, name)
	s.notifyPublish(ctx, name)

	return nil
}
