// Package collections implements collection configuration management for CDC.
//
// This package manages collection configurations stored in MongoDB:
//   - Collection: Represents a MongoDB collection with CDC settings
//   - DestinationConfig: Configures where events are sent
//   - Store: CRUD operations for collection configurations
//
// Key Features:
//   - Per-collection stream enable/disable
//   - Multiple destinations per collection (HTTP, EventBridge)
//   - Event type filtering per destination
//   - Deletion protection flag
//   - TTL attribute configuration
//
// Collection Schema:
//
//	{
//	  "collection_name": "users",
//	  "stream_enabled": true,
//	  "old_image": true,
//	  "ttl_attribute": "expiresAt",
//	  "deletion_protection": true,
//	  "destinations": [...]
//	}
//
// Usage:
//
//	store := collections.NewStore(mongoClient, database)
//
//	// Create collection
//	collection := collections.Collection{
//	    CollectionName:    "users",
//	    StreamEnabled:     true,
//	    OldImage:          true,
//	    Destinations:      []collections.DestinationConfig{...},
//	}
//	if err := store.Create(ctx, collection); err != nil {
//	    log.Fatal(err)
//	}
//
//	// List enabled collections
//	collections, err := store.ListStreamEnabled(ctx)
package collections
