// Package dispatch implements event dispatching to configured sinks.
//
// Components:
//   - Dispatcher: Central event router with per-collection runtime-sink
//     registration. Dispatch fans an event out to all registered sinks in
//     parallel and waits for every delivery to settle.
//   - lane: Per-sink execution lane owned by the dispatcher — a bounded job
//     queue consumed by a small worker pool. Each worker calls
//     RuntimeSink.Send, so delivery to a sink is isolated from every other
//     sink's lane.
//   - RuntimeSink: Glue between a persisted sink and a concrete Transport; owns
//     event-type filtering, filter evaluation and sink identity.
//   - Transport: Runtime interface for delivering stream events; knows only how
//     to deliver, not when or to which identity.
//   - TransportBuilder: Factory function for creating transports from their
//     type-specific configuration.
//
// Usage:
//
//	d := dispatch.NewDispatcher() // default per-sink lane config; use
//	// NewDispatcherWithConfig for a custom queue size / worker count.
//	transport := dispatch.BuildTransport(ctx, "collection", sink.Type, sink.Spec)
//	d.Register("collection", dispatch.NewRuntimeSink(sink, transport))
//
//	// Route a stream record to all registered sinks (delivered in parallel)
//	d.Dispatch(ctx, "collection", record)
//
// Concurrency and settlement:
//
// Dispatch is synchronous from the caller's point of view — it returns only
// after every matching sink has accepted the event (or reported a failure). It
// parallelizes delivery by submitting one job per sink lane concurrently and
// waiting for each to complete, so a slow or blocked sink does not delay the
// others. Each lane has a bounded queue: when it is full, submission blocks
// (bounded backpressure) until a worker frees capacity or the caller's context
// is cancelled — events are never silently dropped. Because Dispatch's nil
// return still means "delivered to every matching sink", the watcher retains
// its single resume-token owner and the retry/DLQ semantics are unchanged;
// delivery concurrency is a per-event optimization, not an asynchronous
// hand-off.
//
// Features:
//   - Multiple sinks per collection, each with its own queue and worker pool
//   - Concurrent delivery across sinks; failure isolation per sink
//   - Per-sink event type filtering and image filtering
//   - Bounded backpressure with no-drop guarantees
//   - Concurrent-safe registration, removal, update and close
package dispatch
