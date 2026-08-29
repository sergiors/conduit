// Package transports provides pluggable runtime transports for delivering
// stream events to external destinations.
//
// Each transport type registers itself via init() using:
//
//	dispatch.RegisterTransport(collections.SinkTypeHTTP, builderFunc)
//
// Each transport owns its own type-specific Spec struct and decodes the opaque
// spec payload from the shared Sink model itself. The runtime transport
// embeds its Spec and only adds runtime state (clients, connections, caches).
// Filtering, event-type selection and sink identity are handled by
// dispatch.RuntimeSink, not by the transport implementation.
//
// Supported transports:
//   - http: POST events to a webhook endpoint (fully implemented, reference)
//   - meilisearch: Index documents in Meilisearch (fully implemented via the
//     official meilisearch-go SDK; awaits task completion so the write is
//     durable before returning)
//   - eventbridge: Publish to AWS EventBridge (fully implemented via the AWS
//     SDK v2 PutEvents API; uses the SDK-resolved region). The region comes from
//     the AWS SDK default region chain (AWS_REGION / shared config), never the
//     spec. Credentials are resolved via the AWS SDK v2 default credential chain
//     (never stored in the spec) and construction fails fast if none resolve.
//
// To add a new transport, create a new file in this package, define its own
// Spec struct, and call RegisterTransport in an init() function.
package transports

import "go.mongodb.org/mongo-driver/bson"

// decodeSpec decodes an opaque spec map into a typed struct.
func decodeSpec(src map[string]interface{}, dst interface{}) error {
	data, err := bson.Marshal(src)
	if err != nil {
		return err
	}
	return bson.Unmarshal(data, dst)
}
