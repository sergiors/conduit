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
	ID                 string               `bson:"_id,omitempty" json:"_id,omitempty"`
	CollectionName     string               `bson:"collectionName,omitempty" json:"collectionName,omitempty"`
	PartitionKey       string               `bson:"partitionKey,omitempty" json:"partitionKey,omitempty"`
	SortKey            string               `bson:"sortKey,omitempty" json:"sortKey,omitempty"`
	StreamEnabled      bool                 `bson:"streamEnabled" json:"streamEnabled"`
	OldImage           bool                 `bson:"oldImage" json:"oldImage"`
	StreamStartedAt    *primitive.Timestamp `bson:"streamStartedAt,omitempty" json:"streamStartedAt,omitempty"`
	TTLAttribute       string               `bson:"ttlAttribute,omitempty" json:"ttlAttribute,omitempty"`
	DeletionProtection bool                 `bson:"deletionProtection" json:"deletionProtection"`
	CreatedAt          time.Time            `bson:"createdAt" json:"createdAt"`
	UpdatedAt          time.Time            `bson:"updatedAt" json:"updatedAt"`
}

// Manager owns the lifecycle and configuration of CDC-monitored collections
// AND their physical MongoDB infrastructure: collections, indexes, TTL,
// change-stream capability, and deletion with state-purge fan-out. It is not
// merely persisted settings — it also creates and drops physical collections,
// ensures key/TTL/stream-capability indexes, and fires OnPurge/OnPublish hooks.
type Manager struct {
	client     *mongo.Client
	database   string
	collection *mongo.Collection
	sinks      *mongo.Collection
	dlq        *mongo.Collection

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

// NewManager creates a new collection manager
func NewManager(client *mongo.Client, database string) *Manager {
	return &Manager{
		client:     client,
		database:   database,
		collection: client.Database(database).Collection("config.collections"),
		sinks:      client.Database(database).Collection("config.sinks"),
		dlq:        client.Database(database).Collection(DLQCollectionName),
	}
}

// notifyPublish fires OnPublish best-effort after a successful configuration
// mutation. It is nil-safe and never returns an error: a committed mutation
// must not be reported as failed because notification bookkeeping hiccuped.
// The hook runs on the caller's context (the same request ctx the handler used
// today); a failure is logged and swallowed.
func (m *Manager) notifyPublish(ctx context.Context, name string) {
	if m.OnPublish == nil {
		return
	}
	if err := m.OnPublish(ctx, name); err != nil {
		log.Printf("failed to publish config change for %s: %v", name, err)
	}
}

// purgeState runs OnPurge best-effort on a DETACHED bounded context (5s) so an
// abandoned request cannot cancel a committed deletion's cleanup. It is
// nil-safe and never returns an error; a failure is logged and swallowed. The
// hook is idempotent, so a failed purge can be retried by re-running it.
func (m *Manager) purgeState(ctx context.Context, name string) {
	if m.OnPurge == nil {
		return
	}
	purgeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.OnPurge(purgeCtx, name); err != nil {
		log.Printf("failed to purge CDC state after deleting collection %s: %v", name, err)
	}
}

// CreateIndex creates the indexes required by the manager.
func (m *Manager) CreateIndex(ctx context.Context) error {
	collectionIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "collectionName", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := m.collection.Indexes().CreateOne(ctx, collectionIndex); err != nil {
		return err
	}

	sinkIndex := mongo.IndexModel{
		Keys: bson.D{{Key: "collectionId", Value: 1}},
	}
	if _, err := m.sinks.Indexes().CreateOne(ctx, sinkIndex); err != nil {
		return err
	}

	// Unique compound index backing the duplicate-sink rejection: two sinks
	// with the same fingerprint (functional identity) for the same collection
	// cannot coexist. This makes the CreateSink pre-check race-safe.
	sinkFingerprintIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "collectionId", Value: 1}, {Key: "fingerprint", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := m.sinks.Indexes().CreateOne(ctx, sinkFingerprintIndex); err != nil {
		return err
	}

	// Unique dedupKey index backing idempotent DLQ persistence: a stale retry
	// item that is re-processed after a failed removal must not create a
	// duplicate DLQ entry.
	dlqDedupIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "dedupKey", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := m.dlq.Indexes().CreateOne(ctx, dlqDedupIndex); err != nil {
		return err
	}

	// Compound index backing the collection-scoped DLQ list query, sorted by
	// failedAt descending for deterministic pagination.
	dlqListIndex := mongo.IndexModel{
		Keys: bson.D{{Key: "collectionName", Value: 1}, {Key: "failedAt", Value: -1}},
	}
	_, err := m.dlq.Indexes().CreateOne(ctx, dlqListIndex)
	return err
}

// createCollection ensures the physical MongoDB collection exists.
func (m *Manager) createCollection(ctx context.Context, collection *Collection) error {
	db := m.client.Database(m.database)
	collectionName := collection.CollectionName

	// Check if collection already exists
	collections, err := db.ListCollectionNames(ctx, bson.M{"name": collectionName})
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}

	if len(collections) > 0 {
		// Collection already exists, just ensure the key index
		if err := m.ensureKeyIndex(ctx, collectionName, collection.PartitionKey, collection.SortKey); err != nil {
			return fmt.Errorf("ensure key index: %w", err)
		}
		return nil
	}

	// Create the collection with pre- and post-image support enabled. This is a
	// permanent capability of every managed collection, enabled exactly once at
	// creation. It is independent of the oldImage runtime flag: MongoDB is
	// always capable of producing pre-images, and Conduit decides at runtime
	// whether to request and forward them. EnableStream additionally ensures
	// the capability for collections created outside this path.
	if err := db.CreateCollection(
		ctx,
		collectionName,
		options.
			CreateCollection().
			SetChangeStreamPreAndPostImages(bson.M{"enabled": true}),
	); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// Insert a dummy document to ensure the collection is not empty
	// (empty collections can cause issues with change streams)
	if _, err := db.Collection(collectionName).
		InsertOne(ctx, bson.M{
			"_id":          "init",
			"_placeholder": true,
			"_created_at":  time.Now(),
		}); err != nil {
		return fmt.Errorf("insert placeholder: %w", err)
	}

	// Remove the placeholder document
	if _, err := db.
		Collection(collectionName).
		DeleteOne(ctx, bson.M{"_id": "init"}); err != nil {
		return fmt.Errorf("delete placeholder: %w", err)
	}

	if err := m.ensureKeyIndex(
		ctx,
		collectionName,
		collection.PartitionKey,
		collection.SortKey,
	); err != nil {
		return fmt.Errorf("ensure key index: %w", err)
	}

	if err := m.applyValidator(
		ctx,
		collectionName,
		collection.PartitionKey,
		collection.SortKey,
	); err != nil {
		return fmt.Errorf("apply validator: %w", err)
	}

	return nil
}

func (m *Manager) applyValidator(
	ctx context.Context,
	collectionName, partitionKey,
	sortKey string,
) error {
	required := make([]string, 0, 2)

	if partitionKey != "" {
		required = append(required, partitionKey)
	}

	if sortKey != "" {
		required = append(required, sortKey)
	}

	if len(required) == 0 {
		return nil
	}

	cmd := bson.D{
		{Key: "collMod", Value: collectionName},
		{
			Key: "validator",
			Value: bson.M{
				"$jsonSchema": bson.M{
					"bsonType": "object",
					"required": required,
				},
			},
		},
	}

	return m.client.
		Database(m.database).
		RunCommand(ctx, cmd).
		Err()
}

func (m *Manager) ensureKeyIndex(
	ctx context.Context,
	collectionName, primaryKey,
	sortKey string,
) error {
	if primaryKey == "" {
		return nil
	}

	if sortKey != "" && sortKey == primaryKey {
		return fmt.Errorf("sort key cannot be the same as primary key")
	}

	keys := bson.D{{Key: primaryKey, Value: 1}}
	indexName := "primaryKeyIdx"

	if sortKey != "" {
		keys = append(keys, bson.E{Key: sortKey, Value: 1})
		indexName = "primarySortKeyIdx"
	}

	indexModel := mongo.IndexModel{
		Keys: keys,
		Options: options.Index().
			SetName(indexName).
			SetUnique(true),
	}

	_, err := m.client.
		Database(m.database).
		Collection(collectionName).
		Indexes().
		CreateOne(ctx, indexModel)

	return err
}

// Create inserts a new collection configuration and creates the MongoDB collection.
// On success, fires OnPublish (best-effort).
func (m *Manager) Create(ctx context.Context, collection *Collection) error {
	name := collection.CollectionName
	if name == "" {
		return NewValidationError("collection name is required")
	}

	if IsReservedCollectionName(name) {
		return NewValidationError("collection name %q is reserved", name)
	}

	if collection.SortKey != "" && collection.PartitionKey == "" {
		return NewValidationError("partitionKey is required when sortKey is defined")
	}

	if collection.PartitionKey != "" && collection.PartitionKey == collection.SortKey {
		return NewValidationError("sortKey cannot be the same as primaryKey")
	}

	// Deletion protection is mandatory on create.
	collection.DeletionProtection = true

	now := time.Now()
	collection.CreatedAt = now
	collection.UpdatedAt = now

	// Create the actual MongoDB collection for CDC monitoring
	if err := m.createCollection(ctx, collection); err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	result, err := m.collection.InsertOne(ctx, collection)
	if err != nil {
		return err
	}

	// Set the ID on the collection so it can be returned to the client
	if objectID, ok := result.InsertedID.(primitive.ObjectID); ok {
		collection.ID = objectID.Hex()
	}

	m.notifyPublish(ctx, name)

	return nil
}

// Get retrieves a collection by name
func (m *Manager) Get(ctx context.Context, name string) (*Collection, error) {
	var collection Collection

	if err := m.collection.
		FindOne(ctx, bson.M{"collectionName": name}).
		Decode(&collection); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCollectionNotFound
		}
		return nil, err
	}

	return &collection, nil
}

// List returns all collection configurations
func (m *Manager) List(ctx context.Context) ([]Collection, error) {
	cursor, err := m.collection.Find(ctx, bson.M{})
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
// Manager.Delete owns the side-effect fan-out via OnPurge then OnPublish, both
// best-effort. Purge runs BEFORE publish: the pub/sub tick may cause the worker
// to disable the collection, so better that the Redis keys are already gone.
// Neither hook is required (nil is a no-op) and neither failure is returned —
// a committed deletion must not be reported as failed because bookkeeping
// hiccuped. Cleanup of the Redis CDC state (resume token, retry queue,
// dead-letter queue) is performed by Delete itself via OnPurge; the hook is
// idempotent, and a failed purge can be retried by deleting the collection
// again or by invoking OnPurge manually. Manager and its hooks are the single
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
func (m *Manager) Delete(ctx context.Context, name string) error {
	// Get collection to check deletion protection
	collection, err := m.Get(ctx, name)
	if err != nil {
		return err
	}

	// Check deletion protection
	if collection.DeletionProtection {
		return ErrDeletionProtectionEnabled
	}

	// Drop the MongoDB collection
	db := m.client.Database(m.database)
	if err := db.Collection(collection.CollectionName).Drop(ctx); err != nil {
		return fmt.Errorf("drop collection: %w", err)
	}

	// Delete associated sinks
	if err := m.deleteSinksByCollectionID(ctx, collection.ID); err != nil {
		return err
	}

	// Delete the configuration
	if _, err = m.collection.DeleteOne(ctx, bson.M{"collectionName": name}); err != nil {
		return err
	}

	// The MongoDB-side deletion has fully succeeded. From here on every side
	// effect is best-effort bookkeeping: its failure must never turn a
	// committed deletion into an error. Purge first, then publish.
	m.purgeState(ctx, name)
	m.notifyPublish(ctx, name)

	return nil
}
