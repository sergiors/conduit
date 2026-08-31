package collections

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// EnableStream enables the CDC stream for a collection and configures old_image.
// oldImage is a runtime behavior only: it tells the watcher whether to request
// and forward pre-images.
//
// When oldImage is true, the physical collection's changeStreamPreAndPostImages
// capability is ensured before the watcher can observe the stream. Collections
// created through Manager.Create always have the capability already; this call
// repairs collections that were created by an older Conduit version or outside
// Conduit, where the capability would otherwise be missing. Without it, MongoDB
// accepts the change stream but silently omits fullDocumentBeforeChange on
// update and replace (and delete) events, and the pre-image is lost at the
// source — no downstream code can recover it. The command is idempotent, and a
// MongoDB-level failure aborts the enablement by rolling the recorded stream
// back: with old_image enabled, a silent capability gap is indistinguishable
// from data loss. The capability check runs after the existence check (an
// unknown collection returns ErrCollectionNotFound without side effects)
// because collMod carries no stream semantics and would otherwise mask the
// domain contract with a driver-level NamespaceNotFound error.
//
// The stream configuration is immutable: if the stream is already enabled, any
// subsequent EnableStream returns ErrStreamAlreadyExists, even with the same
// old_image value. To change it, disable the stream first via DisableStream.
// Returns ErrCollectionNotFound if the collection does not exist.
// On success, fires OnPublish (best-effort).
func (m *Manager) EnableStream(ctx context.Context, name string, oldImage bool) error {
	// Atomic conditional update. It only succeeds when the stream is not enabled
	// yet. Once enabled, old_image cannot be redefined through this route. This
	// also validates existence atomically, before any MongoDB-level side effect.
	filter := bson.M{
		"collection_name": name,
		"stream_enabled":  bson.M{"$ne": true},
	}

	update := bson.M{
		"$set": bson.M{
			"stream_enabled": true,
			"old_image":      oldImage,
			"updated_at":     time.Now(),
		},
	}

	result, err := m.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("set stream: %w", err)
	}

	if result.MatchedCount == 0 {
		// MatchedCount == 0 means either the collection does not exist or the
		// stream is already enabled. Fetch once to return the correct domain error.
		_, err = m.Get(ctx, name)
		if err != nil {
			return err
		}
		return ErrStreamAlreadyExists
	}

	// Ensure the MongoDB pre-image capability once the stream is recorded.
	// If it fails, roll the recorded stream back so the stored configuration
	// stays untouched and the operator can simply retry.
	if oldImage {
		if err := m.ensureChangeStreamPreAndPostImages(ctx, name); err != nil {
			if rollbackErr := m.rollbackStream(ctx, name); rollbackErr != nil {
				return fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
			}
			return err
		}
	}

	m.notifyPublish(ctx, name)
	return nil
}

// rollbackStream undoes a partially-applied EnableStream after the capability
// repair failed, restoring the stored configuration to its pre-call state.
func (m *Manager) rollbackStream(ctx context.Context, name string) error {
	_, err := m.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"stream_enabled": false,
			"old_image":      false,
			"updated_at":     time.Now(),
		}},
	)
	if err != nil {
		return fmt.Errorf("rollback stream for %s: %w", name, err)
	}
	return nil
}

// DisableStream disables the CDC stream and clears old_image for a collection.
// It only updates Conduit metadata; the physical collection keeps its
// changeStreamPreAndPostImages capability (that capability can only be granted,
// never revoked through Conduit, so a later re-enable with old_image works
// immediately). Idempotent. Returns ErrCollectionNotFound if the collection
// does not exist. On success, fires OnPublish (best-effort).
func (m *Manager) DisableStream(ctx context.Context, name string) error {
	if _, err := m.Get(ctx, name); err != nil {
		return err
	}
	_, err := m.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"stream_enabled": false,
			"old_image":      false,
			"updated_at":     time.Now(),
		}},
	)
	if err != nil {
		return err
	}

	m.notifyPublish(ctx, name)
	return nil
}

// ListStreamEnabled returns collections with streams enabled
func (m *Manager) ListStreamEnabled(ctx context.Context) ([]Collection, error) {
	cursor, err := m.collection.Find(ctx, bson.M{"stream_enabled": true})
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

// ensureChangeStreamPreAndPostImages enables the changeStreamPreAndPostImages
// capability on the physical collection via collMod, so MongoDB is able to
// serve fullDocumentBeforeChange to the watcher. It is idempotent: enabling an
// already-enabled capability is a no-op.
//
// Collections created through Manager.Create are provisioned with the
// capability up front; this exists to repair collections that predate that
// provisioning or were created outside Conduit. MongoDB versions before 6.0
// reject the command, in which case the returned error aborts EnableStream —
// enabling a stream with old_image on a deployment that cannot produce
// pre-images would silently breach the event contract.
func (m *Manager) ensureChangeStreamPreAndPostImages(ctx context.Context, name string) error {
	cmd := bson.D{
		{Key: "collMod", Value: name},
		{Key: "changeStreamPreAndPostImages", Value: bson.M{"enabled": true}},
	}
	if err := m.client.Database(m.database).RunCommand(ctx, cmd).Err(); err != nil {
		return fmt.Errorf("enable changeStreamPreAndPostImages for %s: %w", name, err)
	}
	return nil
}
