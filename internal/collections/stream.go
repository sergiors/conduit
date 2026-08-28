package collections

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// EnableStream enables the CDC stream for a collection and configures old_image.
// oldImage is a runtime behavior only: it tells the watcher whether to request
// and forward pre-images. It does not affect MongoDB configuration, because
// changeStreamPreAndPostImages is a permanent capability enabled once at
// collection creation.
//
// The stream configuration is immutable: if the stream is already enabled, any
// subsequent EnableStream returns ErrStreamAlreadyExists, even with the same
// old_image value. To change it, disable the stream first via DisableStream.
// Returns ErrCollectionNotFound if the collection does not exist.
func (s *Settings) EnableStream(ctx context.Context, name string, oldImage bool) error {
	// Atomic conditional update. It only succeeds when the stream is not enabled
	// yet. Once enabled, old_image cannot be redefined through this route.
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

	result, err := s.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("set stream: %w", err)
	}

	if result.MatchedCount == 0 {
		// MatchedCount == 0 means either the collection does not exist or the
		// stream is already enabled. Fetch once to return the correct domain error.
		_, err = s.Get(ctx, name)
		if err != nil {
			return err
		}
		return ErrStreamAlreadyExists
	}

	return nil
}

// DisableStream disables the CDC stream and clears old_image for a collection.
// It only updates Conduit metadata; the physical collection keeps its
// changeStreamPreAndPostImages capability. Idempotent. Returns
// ErrCollectionNotFound if the collection does not exist.
func (s *Settings) DisableStream(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	_, err := s.collection.UpdateOne(
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

	return nil
}

// ListStreamEnabled returns collections with streams enabled
func (s *Settings) ListStreamEnabled(ctx context.Context) ([]Collection, error) {
	cursor, err := s.collection.Find(ctx, bson.M{"stream_enabled": true})
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
