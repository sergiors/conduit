package collections

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// EnableStream enables the CDC stream for a collection and configures old_image.
// oldImage controls whether change events include the pre-image.
//
// old_image is immutable once the stream is enabled: if the stream is already
// enabled, any subsequent EnableStream returns ErrOldImageImmutable, even with
// the same old_image value. To change it, disable the stream first via
// DisableStream. Returns ErrCollectionNotFound if the collection does not exist.
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
		return ErrOldImageImmutable
	}

	// Configure changeStreamPreAndPostImages on the physical collection.
	s.setChangeStreamPreAndPostImages(ctx, name, true)

	return nil
}

// DisableStream disables the CDC stream and clears old_image for a collection.
// Idempotent. Returns ErrCollectionNotFound if the collection does not exist.
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

	// Disable changeStreamPreAndPostImages on the physical collection.
	s.setChangeStreamPreAndPostImages(ctx, name, false)

	return nil
}

// setChangeStreamPreAndPostImages configures changeStreamPreAndPostImages on a
// collection. It is tolerant: if MongoDB does not support the option, the
// failure is logged and ignored.
func (s *Settings) setChangeStreamPreAndPostImages(ctx context.Context, name string, enabled bool) {
	err := s.client.Database(s.database).RunCommand(ctx, bson.M{
		"collMod":                      name,
		"changeStreamPreAndPostImages": bson.M{"enabled": enabled},
	}).Err()
	if err != nil {
		log.Printf("Warning: Failed to configure changeStreamPreAndPostImages for %s: %v", name, err)
	}
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
