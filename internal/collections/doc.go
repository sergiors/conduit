// Package collections implements collection configuration management for CDC.
//
// This package manages collection configurations stored in MongoDB:
//   - Collection: Represents a MongoDB collection with CDC settings
//   - Sink: A sink configuration stored in its own collection
//   - DLQEntry: A dead-letter record for an exhausted retry event, stored in
//     config.dlq
//   - Manager: owns collection lifecycle and their physical MongoDB
//     infrastructure (collections, indexes, TTL, change-stream capability,
//     deletion with state-purge fan-out) and the dead-letter queue (config.dlq)
//
// Key Features:
//   - Per-collection stream enable/disable
//   - Multiple sinks per collection (HTTP, EventBridge, Meilisearch)
//   - Event type filtering per sink
//   - Deletion protection flag
//   - TTL attribute configuration
//   - Dead-letter queue persistence and read access (config.dlq)
//
// Pre-image support is a permanent capability of every managed collection: the
// physical MongoDB collection is created with changeStreamPreAndPostImages
// enabled, and the capability is never disabled afterwards. The oldImage flag
// is a runtime behavior that tells the watcher whether to request and forward
// pre-images; enabling a stream with oldImage also ensures (idempotently) that
// the physical collection has the capability, repairing collections created by
// older Conduit versions or outside Conduit. MongoDB configuration and Conduit
// configuration are therefore almost entirely independent.
//
// Collection Schema:
//
//	{
//	  "collectionName": "users",
//	  "streamEnabled": true,
//	  "oldImage": true,
//	  "streamStartedAt": { "t": 1787932086, "i": 1 },
//	  "ttlAttribute": "expiresAt",
//	  "deletionProtection": true
//	}
//
// streamStartedAt is the first-start checkpoint captured by EnableStream (a
// primitive.Timestamp from the API host clock with increment 1). The watcher
// uses it as startAtOperationTime on its first run (when no resume token exists)
// so events written between enablement and the watcher start are streamed rather
// than skipped. DisableStream unsets it; re-enabling captures a fresh checkpoint.
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
