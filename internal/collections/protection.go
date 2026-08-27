package collections

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// EnableDeletionProtection enables deletion protection for a collection.
//
// The protection configuration is immutable: if protection is already enabled,
// EnableDeletionProtection returns ErrProtectionAlreadyExists. To change it,
// disable protection first via DisableDeletionProtection. Returns
// ErrCollectionNotFound if the collection does not exist.
func (s *Settings) EnableDeletionProtection(ctx context.Context, name string) error {
	collection, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if collection.DeletionProtection {
		return ErrProtectionAlreadyExists
	}

	_, err = s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"deletion_protection": true,
			"updated_at":          time.Now(),
		}},
	)
	return err
}

// DisableDeletionProtection disables deletion protection for a collection.
// Idempotent. Returns ErrCollectionNotFound if the collection does not exist.
func (s *Settings) DisableDeletionProtection(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}

	_, err := s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"deletion_protection": false,
			"updated_at":          time.Now(),
		}},
	)
	return err
}
