// Package destinations provides pluggable CDC event destinations.
//
// Each destination type registers itself via init() using:
//
//	dispatch.RegisterDestination("type", builderFunc)
//
// Supported destinations:
//   - http: POST events to a webhook endpoint
//   - eventbridge: Publish to AWS EventBridge (skeleton)
//   - meilisearch: Index documents in Meilisearch (skeleton)
//
// To add a new destination, create a new file in this package
// and call RegisterDestination in an init() function.
package destinations
