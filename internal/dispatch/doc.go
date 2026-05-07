// Package dispatch implements event dispatching to configured destinations.
//
// This package handles routing CDC events to external systems:
//   - Dispatcher: Central event router with per-collection destination registration
//   - HTTPDestination: Sends events to HTTP endpoints with optional bearer token
//   - EventBridgeDestination: Sends events to AWS EventBridge
//
// Key Features:
//   - Multiple destinations per collection
//   - Event type filtering (INSERT, MODIFY, REMOVE)
//   - Bearer token authentication for HTTP endpoints
//   - Graceful failure handling (one destination failure doesn't block others)
//
// Usage:
//
//	dispatcher := dispatch.NewDispatcher()
//
//	// Register HTTP destination for a collection
//	httpDest, err := dispatch.NewHTTPDestination(
//	    "http://localhost:3000/events",
//	    "my-secret-token",
//	    []string{"INSERT", "MODIFY", "REMOVE"},
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	dispatcher.Register("users", httpDest)
//
//	// Dispatch event
//	err = dispatcher.Dispatch(ctx, "users", record)
package dispatch
