package collections

// Type identifies a sink implementation persisted in config.sinks.
type Type string

const (
	SinkTypeHTTP        Type = "http"
	SinkTypeEventBridge Type = "eventbridge"
	SinkTypeMeilisearch Type = "meilisearch"
)
