// Package redis provides Redis client wrapper for CDC state management.
//
// This package manages all Redis operations for the CDC worker:
//   - Resume tokens: Per-table change stream resume positions
//   - Retry queues: Events pending retry with exponential backoff
//   - Dead Letter Queue (DLQ): Events exceeding max retries
//   - Idempotency keys: Prevent duplicate event processing
//
// Key Naming Conventions:
//
//	cdc:resume:<collectionName>        - Resume token for a table
//	cdc:retry:<collectionName>         - Retry queue (sorted set by nextRetryAt)
//	cdc:dlq:<collectionName>           - Dead letter queue (list)
//	cdc:processed:<eventID>            - Idempotency key (TTL: 24h)
//
// The event ID is deterministic, derived from the MongoDB change event
// (resume token, or clusterTime + documentKey as fallback), so replays of the
// same change always map to the same key.
//
// Usage:
//
//	client, err := redis.NewClient(ctx, redis.DefaultConfig())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	// Get/set resume token
//	token, err := client.GetResumeToken(ctx, "users")
//	err = client.SetResumeToken(ctx, "users", token)
//
//	// Check/set idempotency
//	processed, err := client.IsProcessed(ctx, "users", "event-123")
//	err = client.MarkProcessed(ctx, "users", "event-123", 24*time.Hour)
package redis
