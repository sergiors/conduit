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
//	cdc:resume_token:<tableName>  - Resume token for a table
//	cdc:retry:<tableName>         - Retry queue (sorted set by nextRetryAt)
//	cdc:dlq:<tableName>           - Dead letter queue (list)
//	cdc:processed:<tableName>:<id> - Idempotency key (TTL: 24h)
//	cdc:event:<id>                - Event payload storage
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
