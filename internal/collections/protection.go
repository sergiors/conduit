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
func (m *Manager) EnableDeletionProtection(ctx context.Context, name string) error {
	collection, err := m.Get(ctx, name)
	if err != nil {
		return err
	}

	if collection.DeletionProtection {
		return ErrProtectionAlreadyExists
	}

	_, err = m.collection.UpdateOne(
		ctx,
		bson.M{"collectionName": name},
		bson.M{"$set": bson.M{
			"deletionProtection": true,
			"updatedAt":          time.Now(),
		}},
	)
	return err
}

// DisableDeletionProtection disables deletion protection for a collection.
// Idempotent. Returns ErrCollectionNotFound if the collection does not exist.
func (m *Manager) DisableDeletionProtection(ctx context.Context, name string) error {
	if _, err := m.Get(ctx, name); err != nil {
		return err
	}

	_, err := m.collection.UpdateOne(
		ctx,
		bson.M{"collectionName": name},
		bson.M{"$set": bson.M{
			"deletionProtection": false,
			"updatedAt":          time.Now(),
		}},
	)
	return err
}
