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
// When oldImage is true, the physical collection's changeStreamPreAndPostImages
// capability is ensured first: without it MongoDB silently omits
// fullDocumentBeforeChange, and the pre-image is lost at the source. A MongoDB
// failure rolls the enablement back, and re-enabling a failure is a retry.
// The stream configuration is immutable: changing it requires disable +
// re-enable, so a subsequent EnableStream returns ErrStreamAlreadyExists.
// Returns ErrCollectionNotFound if the collection does not exist.
// On success, fires OnPublish (best-effort).
func (m *Manager) EnableStream(ctx context.Context, name string, oldImage bool) error {
	// Atomic conditional update: only succeeds when the stream is not enabled
	// yet, validating existence before any MongoDB-level side effect.
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
// repair failed.
func (m *Manager) rollbackStream(ctx context.Context, name string) error {
	_, err := m.collection.UpdateOne(
		ctx,
		bson.M{"collectionName": name},
		bson.M{"$set": bson.M{
			"streamEnabled": false,
			"oldImage":      false,
			"updatedAt":     time.Now(),
		},
			"$unset": bson.M{
				"streamStartedAt": "",
			}},
	)
	if err != nil {
		return fmt.Errorf("rollback stream for %s: %w", name, err)
	}
	return nil
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
		bson.M{"$set": bson.M{
			"streamEnabled": false,
			"oldImage":      false,
			"updatedAt":     time.Now(),
		},
			"$unset": bson.M{
				"streamStartedAt": "",
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

// ensureChangeStreamPreAndPostImages enables the changeStreamPreAndPostImages
// capability on the physical collection via collMod, so MongoDB can serve
// fullDocumentBeforeChange to the watcher. Idempotent. MongoDB versions before
// 6.0 reject the command; the error aborts EnableStream — enabling a stream
// with oldImage on a deployment that cannot produce pre-images would silently
// breach the event contract.
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
