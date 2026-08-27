package collections

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SetTTL sets the collection TTL attribute and creates the TTL index.
//
// The TTL configuration is immutable: once it is set, any subsequent SetTTL
// returns ErrTTLAlreadyExists, even with the same value. To change it, disable
// TTL first via DisableTTL. An empty attribute returns ErrValidation.
func (s *Settings) SetTTL(ctx context.Context, name, attribute string) error {
	if attribute == "" {
		return NewValidationError("ttl attribute is required")
	}

	collection, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if collection.TTLAttribute != "" {
		return ErrTTLAlreadyExists
	}

	if err := s.createTTLIndex(ctx, name, attribute); err != nil {
		return fmt.Errorf("create ttl index: %w", err)
	}

	_, err = s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"ttl_attribute": attribute,
			"updated_at":    time.Now(),
		}},
	)
	if err != nil {
		return fmt.Errorf("set ttl attribute: %w", err)
	}
	return nil
}

// DisableTTL drops the TTL index and clears the TTL attribute.
// Idempotent. Returns ErrCollectionNotFound if the collection does not exist.
func (s *Settings) DisableTTL(ctx context.Context, name string) error {
	collection, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if collection.TTLAttribute == "" {
		return nil
	}

	if err := s.dropTTLIndex(ctx, name, collection.TTLAttribute); err != nil {
		return fmt.Errorf("drop ttl index: %w", err)
	}

	_, err = s.collection.UpdateOne(
		ctx,
		bson.M{"collection_name": name},
		bson.M{"$set": bson.M{
			"ttl_attribute": "",
			"updated_at":    time.Now(),
		}},
	)
	if err != nil {
		return err
	}

	return nil
}

// createTTLIndex creates a TTL index on a collection.
func (s *Settings) createTTLIndex(ctx context.Context, collection, field string) error {
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: field, Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	}
	_, err := s.client.Database(s.database).Collection(collection).Indexes().CreateOne(ctx, indexModel)
	return err
}

// dropTTLIndex drops the TTL index (named <field>_1 by MongoDB default naming)
// from a collection. Tolerant: a missing index is not an error (idempotent).
func (s *Settings) dropTTLIndex(ctx context.Context, collection, field string) error {
	name := field + "_1"
	idxs, err := s.client.Database(s.database).Collection(collection).Indexes().ListSpecifications(ctx)
	if err != nil {
		return fmt.Errorf("list indexes: %w", err)
	}
	for _, idx := range idxs {
		if idx.Name == name {
			if _, err := s.client.Database(s.database).Collection(collection).Indexes().DropOne(ctx, name); err != nil {
				return fmt.Errorf("drop ttl index: %w", err)
			}
			return nil
		}
	}
	return nil
}
