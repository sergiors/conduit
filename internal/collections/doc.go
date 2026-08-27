// Package collections implements collection configuration management for CDC.
//
// This package manages collection configurations stored in MongoDB:
//   - Collection: Represents a MongoDB collection with CDC settings
//   - Sink: A sink configuration stored in its own collection
//   - SinkConfig: Configures where events are sent
//   - Settings: CRUD operations for collection configurations
//
// Key Features:
//   - Per-collection stream enable/disable
//   - Multiple sinks per collection (HTTP, EventBridge, Meilisearch)
//   - Event type filtering per sink
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
//	  "deletion_protection": true
//	}
//
// Usage:
//
//	settings := collections.NewSettings(mongoClient, database)
//
//	// Create collection
//	collection := collections.Collection{
//	    CollectionName:    "users",
//	    StreamEnabled:     true,
//	    OldImage:          true,
//	}
//	if err := settings.Create(ctx, collection); err != nil {
//	    log.Fatal(err)
//	}
//
//	// List enabled collections
//	collections, err := settings.ListStreamEnabled(ctx)
package collections
