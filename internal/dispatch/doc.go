// Package dispatch implements event dispatching to configured sinks.
//
// Components:
//   - Dispatcher: Central event router with per-collection sink registration
//   - Sink: Interface for individual event consumers
//   - SinkBuilder: Factory function for creating sinks from configuration
//
// Usage:
//
//	d := dispatch.NewDispatcher()
//	d.Register("collection", dispatch.BuildSink(ctx, "collection", sinkConfig))
//
//	// Route a stream record to all registered sinks
//	d.Dispatch(ctx, "collection", record)
//
// Features:
//   - Multiple sinks per collection
//   - Per-sink event type filtering
//   - Graceful failure handling (one sink failure doesn't block others)
//   - Concurrent-safe registration and removal
package dispatch
