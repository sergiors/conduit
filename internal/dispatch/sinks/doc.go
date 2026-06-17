// Package sinks provides pluggable CDC event sinks.
//
// Each sink type registers itself via init() using:
//
//	dispatch.RegisterSink("type", builderFunc)
//
// Supported sinks:
//   - http: POST events to a webhook endpoint
//   - eventbridge: Publish to AWS EventBridge (skeleton)
//   - meilisearch: Index documents in Meilisearch (skeleton)
//
// To add a new sink, create a new file in this package
// and call RegisterSink in an init() function.
package sinks
