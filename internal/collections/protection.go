package collections

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// SetDeletionProtection enables or disables deletion protection for a collection.
// It is idempotent: setting a collection to its current protection state is a no-op.
// Returns ErrCollectionNotFound if the collection does not exist.
func (s *Settings) SetDeletionProtection(ctx context.Context, name string, enabled bool) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}

	_, err := s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"deletion_protection": enabled,
			"updated_at":          time.Now(),
		}},
	)
	return err
}
