// Package retry implements retry queue processing with exponential backoff.
//
// This package handles failed event dispatches:
//   - Exponential backoff: 1s, 2s, 4s, 8s, 16s (capped at 5m)
//   - Max retries: 5 attempts
//   - Dead Letter Queue (DLQ): Events exceeding max retries are persisted to
//     MongoDB (config.dlq) before the retry item is removed from Redis. The
//     DLQ is owned by collections.Manager, which the processor receives as a
//     narrow DLQ dependency.
//
// Key Features:
//   - Configurable retry interval (default: 5s)
//   - Per-collection retry queues (Redis-backed)
//   - DLQ monitoring support
//
// Usage:
//
//	processor := retry.NewProcessor(redisClient, collectionsManager, dispatcher, retry.DefaultConfig())
//	if err := processor.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// Retry Flow:
//  1. Event dispatch fails
//  2. Event queued with retryCount=0, nextRetryAt=now+1s
//  3. Processor dequeues when nextRetryAt <= now
//  4. Retry dispatch
//  5. On success: event removed from queue
//  6. On failure: re-queue with incremented retryCount
//  7. After 5 failures: persist to the MongoDB DLQ, then remove from queue
package retry
