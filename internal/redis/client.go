package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps Redis client with CDC-specific operations
type Client struct {
	client *redis.Client
}

// Config holds Redis connection configuration
type Config struct {
	URI      string // Full Redis URI (e.g., redis://user:pass@host:port/db)
	Addr     string // Alternative: host:port
	Password string // Alternative: password
	DB       int    // Database number
	Prefix   string // Key prefix, e.g., "cdc:"
}

// DefaultConfig returns a configuration with empty defaults
// Note: URI or Addr MUST be provided - no defaults for connection
func DefaultConfig() Config {
	return Config{
		URI:      "",
		Addr:     "",
		Password: "",
		DB:       0,
		Prefix:   "cdc:",
	}
}

// NewClient creates a new Redis client
// Supports both URI (DSN) and separate Addr/Password configurations
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	var client *redis.Client

	if cfg.URI != "" {
		// Use URI/DSN format (e.g., redis://user:password@host:port/db)
		opts, err := redis.ParseURL(cfg.URI)
		if err != nil {
			return nil, fmt.Errorf("parse redis URI: %w", err)
		}
		client = redis.NewClient(opts)
	} else if cfg.Addr != "" {
		// Use separate Addr/Password
		client = redis.NewClient(&redis.Options{
			Addr:     cfg.Addr,
			Password: cfg.Password,
			DB:       cfg.DB,
		})
	} else {
		return nil, fmt.Errorf("redis URI or Addr must be provided")
	}

	// Verify connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Client{
		client: client,
	}, nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.client.Close()
}

// Key helpers
func (c *Client) resumeTokenKey(collectionName string) string {
	return "cdc:resume:" + collectionName
}

func (c *Client) retryQueueKey(collectionName string) string {
	return "cdc:retry:" + collectionName
}

func (c *Client) dlqKey(collectionName string) string {
	return "cdc:dlq:" + collectionName
}

func (c *Client) processedKey(id string) string {
	return "cdc:processed:" + id
}

// ResumeToken operations
// GetResumeToken retrieves the resume token for a collection
func (c *Client) GetResumeToken(ctx context.Context, collectionName string) (string, error) {
	key := c.resumeTokenKey(collectionName)
	token, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil // No token exists
	}
	if err != nil {
		return "", fmt.Errorf("get resume token: %w", err)
	}
	return token, nil
}

// SetResumeToken sets the resume token for a collection
// Only call this after successful event processing
func (c *Client) SetResumeToken(ctx context.Context, collectionName, token string) error {
	key := c.resumeTokenKey(collectionName)
	return c.client.Set(ctx, key, token, 0).Err()
}

// DeleteResumeToken removes the resume token for a collection
func (c *Client) DeleteResumeToken(ctx context.Context, collectionName string) error {
	key := c.resumeTokenKey(collectionName)
	return c.client.Del(ctx, key).Err()
}

// Idempotency operations
// IsProcessed checks if an event has already been processed
func (c *Client) IsProcessed(ctx context.Context, id string) (bool, error) {
	key := c.processedKey(id)
	exists, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("check processed: %w", err)
	}
	return exists == 1, nil
}

// MarkProcessed marks an event as processed with TTL
func (c *Client) MarkProcessed(ctx context.Context, id string, ttl time.Duration) error {
	key := c.processedKey(id)
	return c.client.Set(ctx, key, "1", ttl).Err()
}

// RetryQueue operations
// RetryEvent adds an event to the retry queue with retry count
type RetryEvent struct {
	ID               string          `json:"id"`
	CollectionName   string          `json:"collectionName"`
	EventData        json.RawMessage `json:"eventData"` // Raw JSON of the stream record
	RetryCount       int             `json:"retryCount"`
	MaxRetries       int             `json:"maxRetries"`
	NextRetryAt      time.Time       `json:"nextRetryAt"`
} 

// EnqueueRetry adds an event to the retry queue with exponential backoff
// The event JSON is stored directly as the member in the sorted set
func (c *Client) EnqueueRetry(ctx context.Context, event RetryEvent) error {
	key := c.retryQueueKey(event.CollectionName)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal retry event: %w", err)
	}

	// Use sorted set with nextRetryAt as score and JSON as member
	score := float64(event.NextRetryAt.UnixNano())
	return c.client.ZAdd(ctx, key, redis.Z{
		Score:  score,
		Member: data,
	}).Err()
}

// DequeueRetry gets events ready for retry (nextRetryAt <= now)
// Events are NOT removed from queue - caller must remove after processing
func (c *Client) DequeueRetry(ctx context.Context, collectionName string, limit int64) ([]RetryEvent, error) {
	key := c.retryQueueKey(collectionName)
	now := time.Now().UnixNano()

	// Get events with score <= now
	members, err := c.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min:   "0",
		Max:   fmt.Sprintf("%d", now),
		Count: limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("dequeue retry: %w", err)
	}

	events := make([]RetryEvent, 0, len(members))
	for _, member := range members {
		var event RetryEvent
		if err := json.Unmarshal([]byte(member), &event); err != nil {
			return nil, fmt.Errorf("unmarshal retry event: %w", err)
		}
		events = append(events, event)
	}

	return events, nil
}

// RemoveRetryEvent removes a processed event from the retry queue
func (c *Client) RemoveRetryEvent(ctx context.Context, collectionName string, event RetryEvent) error {
	key := c.retryQueueKey(collectionName)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal retry event: %w", err)
	}
	return c.client.ZRem(ctx, key, string(data)).Err()
}

// DLQ operations
// SendToDLQ adds an event to the dead letter queue
func (c *Client) SendToDLQ(ctx context.Context, collectionName string, event interface{}) error {
	key := c.dlqKey(collectionName)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal DLQ event: %w", err)
	}

	// Push to list (DLQ)
	return c.client.RPush(ctx, key, data).Err()
}

// GetDLQLength returns the number of events in the DLQ
func (c *Client) GetDLQLength(ctx context.Context, collectionName string) (int64, error) {
	key := c.dlqKey(collectionName)
	return c.client.LLen(ctx, key).Result()
}

// Monitor operations
// GetRetryQueueLength returns the number of events in the retry queue
func (c *Client) GetRetryQueueLength(ctx context.Context, collectionName string) (int64, error) {
	key := c.retryQueueKey(collectionName)
	return c.client.ZCard(ctx, key).Result()
}

// Health check
func (c *Client) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Pub/Sub operations
// PublishConfigChange publishes a collection change notification
// payload can be collectionName or collectionID - receiver decides how to interpret
func (c *Client) PublishConfigChange(ctx context.Context, collectionName string) error {
	channel := "cdc:config-change"
	return c.client.Publish(ctx, channel, collectionName).Err()
}

// SubscribeConfigChanges subscribes to collection change notifications
func (c *Client) SubscribeConfigChanges(ctx context.Context) (*redis.PubSub, error) {
	channel := "cdc:config-change"
	pubsub := c.client.Subscribe(ctx, channel)

	// Wait for subscription confirmation
	_, err := pubsub.Receive(ctx)
	if err != nil {
		pubsub.Close()
		return nil, fmt.Errorf("subscribe to config changes: %w", err)
	}

	return pubsub, nil
}
