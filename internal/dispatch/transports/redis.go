package transports

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// RedisSpec holds the type-specific configuration for a Redis Streams transport.
type RedisSpec struct {
	URL    string `bson:"url" json:"url"`
	Stream string `bson:"stream" json:"stream"`
}

// xaddTimeout bounds the XADD call. The dispatcher passes the watcher's
// long-lived context into Send, so each transport must bound its own delivery;
// go-redis imposes no deadline beyond ctx cancellation. 30s is a generous budget
// for a single XADD request.
const xaddTimeout = 30 * time.Second

// XAdder is the minimal interface RedisTransport needs from the go-redis
// client; *redis.Client satisfies it. A narrow interface keeps the transport
// testable without a live Redis.
type XAdder interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
	Close() error
}

// RedisTransport delivers stream records to a Redis Stream via XADD.
type RedisTransport struct {
	RedisSpec

	client XAdder
}

// NewRedis builds a Redis transport from its spec.
func NewRedis(ctx context.Context, spec RedisSpec, logger *log.Logger) dispatch.Transport {
	if spec.URL == "" {
		logger.Printf("Redis transport requires a url")
		return nil
	}
	if spec.Stream == "" {
		logger.Printf("Redis transport requires a stream")
		return nil
	}

	opts, err := redis.ParseURL(spec.URL)
	if err != nil {
		logger.Printf("Redis transport: invalid url %q: %v", spec.URL, err)
		return nil
	}

	// Build the client once at construction. Deliberately do NOT ping here and
	// do NOT connect per event: the transport must be constructible even when
	// Redis is down, so failures surface on Send and flow to the retry pipeline.
	// Construction-time connectivity probing would break the fail-closed model —
	// BuildTransport wraps a nil return in an unavailableTransport whose static
	// error can never recover, whereas a built transport reconnects automatically
	// when Redis returns (go-redis clients reconnect on demand).
	client := redis.NewClient(opts)

	return &RedisTransport{
		RedisSpec: spec,
		client:    client,
	}
}

// Send appends a stream record to the configured Redis Stream via XADD. The
// record is serialized with json.Marshal (canonical event JSON, preserving
// nested newImage/oldImage) and stored in a single field named "event". The
// entry ID is left empty so Redis auto-generates it ("*"); no MaxLen is set, so
// the stream is never trimmed. Success means XADD returned without error.
func (t *RedisTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("redis: marshal record %s (table %s): %w", record.EventID, record.TableName, err)
	}

	xaddCtx, cancel := context.WithTimeout(ctx, xaddTimeout)
	defer cancel()

	cmd := t.client.XAdd(xaddCtx, &redis.XAddArgs{
		Stream: t.Stream,
		Values: map[string]interface{}{"event": string(payload)},
	})
	if err := cmd.Err(); err != nil {
		return fmt.Errorf("redis: xadd for record %s (table %s): %w", record.EventID, record.TableName, err)
	}
	return nil
}

// Close closes the underlying Redis client. The transport owns the client
// lifecycle.
func (t *RedisTransport) Close() error {
	return t.client.Close()
}

// buildRedis decodes a raw spec and builds a Redis transport. Unlike
// Meilisearch's optional indexName, the stream is required and is never
// defaulted to the collection name.
func buildRedis(ctx context.Context, collectionName string, t collections.Type, rawSpec map[string]interface{}, logger *log.Logger) dispatch.Transport {
	var spec RedisSpec
	if err := decodeSpec(rawSpec, &spec); err != nil {
		logger.Printf("Failed to decode Redis transport spec for %s: %v", collectionName, err)
		return nil
	}

	return NewRedis(ctx, spec, logger)
}

func init() {
	dispatch.RegisterTransport(collections.SinkTypeRedis, buildRedis)
}
