// Package watcher implements CDC watcher management for MongoDB collections.
//
// The watcher package provides:
//   - Manager: Centralized watcher lifecycle management
//   - Watcher: Per-table change stream watcher with resume token support
//
// Key Features:
//   - One watcher per table (no duplicates)
//   - Resume token management per table (stored in Redis)
//   - Graceful start/stop with no goroutine leaks
//   - Automatic sync with config.tables configuration
//
// Usage:
//
//	manager := watcher.NewManager(mongoClient, database, tableStore, redisClient, dispatcher, watcher.DefaultConfig())
//	if err := manager.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer manager.Stop(ctx)
//
// Stream Activation:
//
// Watchers are only created for tables with stream_enabled=true in config.tables.
// The manager periodically syncs (every 30s by default) to detect changes.
package watcher
