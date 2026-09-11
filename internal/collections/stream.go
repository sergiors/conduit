package collections

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// EnableStream enables the CDC stream for a collection and configures oldImage.
//
// The changeStreamPreAndPostImages capability is a permanent property of every
// managed collection, provisioned exactly once at creation (see
// createCollection); EnableStream never modifies it. When oldImage is true,
// EnableStream only VERIFIES that the physical collection was created with the
// capability — without it MongoDB silently omits fullDocumentBeforeChange and
// the pre-image is lost at the source. Because verification precedes
// persistence, a failed verification leaves config.collections untouched (no
// rollback needed). The stream configuration is immutable: changing it requires
// disable + re-enable, so a subsequent EnableStream returns
// ErrStreamAlreadyExists. Immutability (ErrStreamAlreadyExists) is checked
// BEFORE capability verification: a re-enable attempt is an immutability
// violation regardless of the physical collection's state. Returns
// ErrCollectionNotFound if the collection does not exist. On success, fires
// OnPublish (best-effort).
func (m *Manager) EnableStream(ctx context.Context, name string, oldImage bool) error {
	// Verify the collection configuration exists before any MongoDB-level
	// work; EnableStream is called by both the API and admin paths and unknown
	// collections must surface ErrCollectionNotFound.
	cfg, err := m.Get(ctx, name)
	if err != nil {
		return err
	}

	// The stream configuration is immutable. Guard immutability up front,
	// BEFORE capability verification: a re-enable attempt is an immutability
	// violation (409) even when the physical collection lacks the pre-image
	// capability (which would otherwise surface as a 400 validation error). A
	// change requires disable + re-enable.
	if cfg.StreamEnabled {
		return ErrStreamAlreadyExists
	}

	// oldImage requires the physical collection to already carry the
	// changeStreamPreAndPostImages capability. This check is read-only and runs
	// BEFORE persistence, so a failure never leaves the stream enabled. The
	// physical collection may exist while its config document is validated (it
	// was, in step 1) yet lack the capability — typically because it was created
	// outside Conduit. Recreate it through Conduit to gain the capability.
	if oldImage {
		enabled, err := m.hasChangeStreamPreAndPostImages(ctx, name)
		if err != nil {
			return err
		}
		if !enabled {
			return NewValidationError(
				"collection %q does not have changeStreamPreAndPostImages enabled; recreate the collection through Conduit to enable oldImage streams",
				name,
			)
		}
	}

	// Atomic conditional update: only succeeds when the stream is not enabled
	// yet. Existence was already confirmed above, so MatchedCount == 0 can only
	// mean the stream is already enabled (immutability of stream configuration).
	filter := bson.M{
		"collectionName": name,
		"streamEnabled":  bson.M{"$ne": true},
	}

	// First-start checkpoint: derives from the API host clock, assuming
	// reasonable alignment with the MongoDB cluster clock (drift only shifts
	// the replay anchor). Consumed in watcher.buildChangeStreamOptions.
	checkpoint := primitive.Timestamp{T: uint32(time.Now().Unix()), I: 1}

	update := bson.M{
		"$set": bson.M{
			"streamEnabled":   true,
			"oldImage":        oldImage,
			"streamStartedAt": checkpoint,
			"updatedAt":       time.Now(),
		},
	}

	result, err := m.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("set stream: %w", err)
	}

	if result.MatchedCount == 0 {
		_, err = m.Get(ctx, name)
		if err != nil {
			return err
		}
		return ErrStreamAlreadyExists
	}

	m.notifyPublish(ctx, name)
	return nil
}

// hasChangeStreamPreAndPostImages reports whether the physical collection was
// created with changeStreamPreAndPostImages enabled. It is read-only and never
// modifies the collection: the capability is granted at creation and Conduit
// never enables it afterwards. A missing physical collection is a corrupted
// state — its config document exists (EnableStream already validated it) but
// the collection does not — so we wrap the error rather than treat it as a
// benign "not enabled".
func (m *Manager) hasChangeStreamPreAndPostImages(ctx context.Context, name string) (bool, error) {
	specs, err := m.client.Database(m.database).ListCollectionSpecifications(ctx, bson.M{"name": name})
	if err != nil {
		return false, fmt.Errorf("list collection specifications for %s: %w", name, err)
	}
	if len(specs) == 0 {
		return false, fmt.Errorf("physical collection %q does not exist: %w", name, ErrCollectionNotFound)
	}

	if len(specs[0].Options) == 0 {
		return false, nil
	}

	var opts struct {
		ChangeStreamPreAndPostImages struct {
			Enabled bool `bson:"enabled"`
		} `bson:"changeStreamPreAndPostImages"`
	}
	if err := bson.Unmarshal(specs[0].Options, &opts); err != nil {
		return false, fmt.Errorf("parse collection options for %s: %w", name, err)
	}
	return opts.ChangeStreamPreAndPostImages.Enabled, nil
}

// DisableStream disables the CDC stream and clears oldImage — and unsets the
// first-start checkpoint, so a re-enable captures a fresh one. The physical
// collection keeps its changeStreamPreAndPostImages capability (it can only be
// granted, never revoked through Conduit). Idempotent; fires OnPublish
// (best-effort).
func (m *Manager) DisableStream(ctx context.Context, name string) error {
	if _, err := m.Get(ctx, name); err != nil {
		return err
	}
	_, err := m.collection.UpdateOne(
		ctx,
		bson.M{"collectionName": name},
		bson.M{
			"$set": bson.M{
				"streamEnabled": false,
				"oldImage":      false,
				"updatedAt":     time.Now(),
			},
			"$unset": bson.M{
				"streamStartedAt": "",
			},
		},
	)
	if err != nil {
		return err
	}

	m.notifyPublish(ctx, name)
	return nil
}

// ListStreamEnabled returns collections with streams enabled
func (m *Manager) ListStreamEnabled(ctx context.Context) ([]Collection, error) {
	cursor, err := m.collection.Find(ctx, bson.M{"streamEnabled": true})
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
