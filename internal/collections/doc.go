// Package collections implements collection configuration management for CDC.
//
// This package manages collection configurations stored in MongoDB:
//   - Collection: Represents a MongoDB collection with CDC settings
//   - Sink: A sink configuration stored in its own collection
//   - Manager: owns collection lifecycle and their physical MongoDB
//     infrastructure (collections, indexes, TTL, change-stream capability,
//     deletion with state-purge fan-out)
//
// Key Features:
//   - Per-collection stream enable/disable
//   - Multiple sinks per collection (HTTP, EventBridge, Meilisearch)
//   - Event type filtering per sink
//   - Deletion protection flag
//   - TTL attribute configuration
//
// Pre-image support is a permanent capability of every managed collection: the
// physical MongoDB collection is created with changeStreamPreAndPostImages
// enabled, and the capability is never disabled afterwards. The old_image flag
// is a runtime behavior that tells the watcher whether to request and forward
// pre-images; enabling a stream with old_image also ensures (idempotently) that
// the physical collection has the capability, repairing collections created by
// older Conduit versions or outside Conduit. MongoDB configuration and Conduit
// configuration are therefore almost entirely independent.
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
//	manager := collections.NewManager(mongoClient, database)
//
//	// Create collection
//	collection := collections.Collection{
//	    CollectionName:    "users",
//	    StreamEnabled:     true,
//	    OldImage:          true,
//	}
//	if err := manager.Create(ctx, collection); err != nil {
//	    log.Fatal(err)
//	}
//
//	// List enabled collections
//	collections, err := manager.ListStreamEnabled(ctx)
package collections
