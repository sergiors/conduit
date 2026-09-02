package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DLQCollectionName is the physical MongoDB collection that stores dead-letter
// entries for exhausted retry events. It is a manager-owned internal namespace
// and is reserved so it can never be shadowed by a user-created CDC collection.
const DLQCollectionName = "config.dlq"

// DLQEntry is a persisted dead-letter record for an exhausted retry event.
//
// SinkID is intentionally not populated: the current retry event shape does not
// carry a per-sink identifier, and the dispatcher returns only an aggregate
// error (not per-sink failures), so the originating sink cannot be determined
// reliably. It is stored empty/unknown rather than over-engineered.
type DLQEntry struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"_id,omitempty"`
	CollectionName string             `bson:"collectionName" json:"collectionName"`
	SinkID         string             `bson:"sinkId,omitempty" json:"sinkId,omitempty"`
	EventData      json.RawMessage    `bson:"eventData" json:"eventData"`
	LastError      string             `bson:"lastError,omitempty" json:"lastError,omitempty"`
	FailedAt       time.Time          `bson:"failedAt" json:"failedAt"`
	// DedupKey is the retry event ID, which already carries the collection
	// prefix (e.g. "users:<token>"). A unique index on it makes CreateDLQEntry
	// idempotent: if the retry item removal fails and the event is re-processed,
	// the second persist is a no-op instead of a duplicate DLQ entry.
	DedupKey string `bson:"dedupKey" json:"dedupKey"`
}

// DLQListOptions controls the bounded result set returned by ListDLQEntries.
type DLQListOptions struct {
	// Limit caps the number of entries returned. Zero means no limit.
	Limit int64
	// Skip is the number of entries to skip before returning results.
	Skip int64
}

// CreateDLQEntry writes a dead-letter entry. It is idempotent on DedupKey: a
// second write for the same key is a no-op (the existing entry is kept), so a
// stale retry item that is re-processed after a failed removal does not create
// an unbounded number of duplicate DLQ entries. A failed write is surfaced as
// an error so the caller (the retry processor) can keep the retry item
// recoverable.
func (m *Manager) CreateDLQEntry(ctx context.Context, entry DLQEntry) error {
	// Upsert on the dedup key: on a duplicate, keep the original entry (the
	// first terminal record) rather than overwriting it.
	opts := options.Update().SetUpsert(true)
	filter := bson.M{"dedupKey": entry.DedupKey}
	update := bson.M{"$setOnInsert": entry}
	if _, err := m.dlq.UpdateOne(ctx, filter, update, opts); err != nil {
		return fmt.Errorf("persist dlq entry: %w", err)
	}
	return nil
}

// ListDLQEntries returns dead-letter entries for a managed collection, bounded
// by opts and sorted by failedAt descending for deterministic pagination. An
// empty result set is returned as an empty (non-nil) slice. The collection is
// validated through Get before the DLQ is queried, so unmanaged collections
// never touch config.dlq.
func (m *Manager) ListDLQEntries(ctx context.Context, collectionName string, opts DLQListOptions) ([]DLQEntry, error) {
	if _, err := m.Get(ctx, collectionName); err != nil {
		return nil, err
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "failedAt", Value: -1}}).
		SetSkip(opts.Skip)
	if opts.Limit > 0 {
		findOpts.SetLimit(opts.Limit)
	}

	cursor, err := m.dlq.Find(ctx, bson.M{"collectionName": collectionName}, findOpts)
	if err != nil {
		return nil, fmt.Errorf("find dlq entries: %w", err)
	}
	defer cursor.Close(ctx)

	var entries []DLQEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, fmt.Errorf("decode dlq entries: %w", err)
	}
	if entries == nil {
		entries = []DLQEntry{}
	}
	return entries, nil
}

// GetDLQEntry returns a single dead-letter entry by ID for a managed
// collection, but only if it belongs to the requested collection. Returns
// ErrDLQEntryNotFound when the entry does not exist or belongs to a different
// collection. The collection is validated through Get before the DLQ is
// queried, so unmanaged collections never touch config.dlq.
func (m *Manager) GetDLQEntry(ctx context.Context, collectionName, id string) (*DLQEntry, error) {
	if _, err := m.Get(ctx, collectionName); err != nil {
		return nil, err
	}

	objectID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, ErrDLQEntryNotFound
	}

	var entry DLQEntry
	err = m.dlq.FindOne(ctx, bson.M{
		"_id":            objectID,
		"collectionName": collectionName,
	}).Decode(&entry)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrDLQEntryNotFound
		}
		return nil, fmt.Errorf("find dlq entry: %w", err)
	}
	return &entry, nil
}

// CountDLQEntries returns the number of dead-letter entries for a managed
// collection. The collection is validated through Get before the DLQ is
// queried, so unmanaged collections never touch config.dlq.
func (m *Manager) CountDLQEntries(ctx context.Context, collectionName string) (int64, error) {
	if _, err := m.Get(ctx, collectionName); err != nil {
		return 0, err
	}

	count, err := m.dlq.CountDocuments(ctx, bson.M{"collectionName": collectionName})
	if err != nil {
		return 0, fmt.Errorf("count dlq entries: %w", err)
	}
	return count, nil
}
