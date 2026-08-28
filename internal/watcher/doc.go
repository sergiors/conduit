// Package watcher implements CDC watcher management for MongoDB collections.
//
// The watcher package provides:
//   - Manager: Centralized watcher lifecycle management
//   - Watcher: Per-collection change stream watcher with resume token support
//
// Key Features:
//   - One watcher per collection (no duplicates)
//   - Resume token management per collection (stored in Redis)
//   - Resume tokens advance only after successful processing and are never
//     deleted on transient errors; they are invalidated only when MongoDB
//     explicitly rejects the token as no longer usable
//   - Graceful start/stop with no goroutine leaks
//   - Automatic sync with config.collections configuration
//
// Usage:
//
//	manager := watcher.NewManager(mongoClient, database, collectionSettings, redisClient, dispatcher, watcher.DefaultConfig())
//	if err := manager.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer manager.Stop(ctx)
//
// Stream Activation:
//
// Watchers are only created for collections with stream_enabled=true in config.collections.
// The manager periodically syncs (every 30s by default) to detect changes.
package watcher
