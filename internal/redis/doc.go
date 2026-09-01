// Package redis provides Redis client wrapper for CDC state management.
//
// This package manages the Redis operations for the CDC worker:
//   - Resume tokens: Per-table change stream resume positions
//   - Retry queues: Events pending retry with exponential backoff
//   - Idempotency keys: Prevent duplicate event processing
//
// The dead-letter queue is NOT stored in Redis. Exhausted retry events are
// persisted to MongoDB (config.dlq) by the retry processor; see the dlq
// package.
//
// Key Naming Conventions:
//
//	cdc:resume:<collectionName>        - Resume token for a table
//	cdc:retry:<collectionName>         - Retry queue (sorted set by nextRetryAt)
//	cdc:processed:<eventID>            - Idempotency key (TTL: configurable, default 24h)
//
// The event ID is deterministic, derived from the MongoDB change event
// (resume token, or clusterTime + documentKey as fallback), so replays of the
// same change always map to the same key.
//
// Idempotency is best-effort and bounded by the processed-key TTL (default
// 24h, configurable via PROCESSED_EVENT_TTL). Delivery is at-least-once: a
// duplicate may be delivered if the processed key has expired, if the
// MarkProcessed write is lost, or if Redis is unavailable. Downstream
// consumers must be idempotent using the event ID.
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
