// Package dispatch implements event dispatching to configured sinks.
//
// Components:
//   - Dispatcher: Central event router with per-collection runtime-sink registration
//   - RuntimeSink: Glue between a persisted sink and a concrete Transport; owns
//     event-type filtering, filter evaluation and sink identity
//   - Transport: Runtime interface for delivering stream events; knows only how
//     to deliver, not when or to which identity
//   - TransportBuilder: Factory function for creating transports from their
//     type-specific configuration
//
// Usage:
//
//	d := dispatch.NewDispatcher()
//	transport := dispatch.BuildTransport(ctx, "collection", sink.Type, sink.Spec)
//	d.Register("collection", dispatch.NewRuntimeSink(sink, transport))
//
//	// Route a stream record to all registered sinks
//	d.Dispatch(ctx, "collection", record)
//
// Features:
//   - Multiple sinks per collection
//   - Per-sink event type filtering and image filtering
//   - Graceful failure handling (one sink failure doesn't block others)
//   - Concurrent-safe registration and removal
package dispatch
