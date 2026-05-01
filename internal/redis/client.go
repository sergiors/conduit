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
func (c *Client) resumeTokenKey(tableName string) string {
	return "cdc:resume_token:" + tableName
}

func (c *Client) retryQueueKey(tableName string) string {
	return "cdc:retry:" + tableName
}

func (c *Client) dlqKey(tableName string) string {
	return "cdc:dlq:" + tableName
}

func (c *Client) processedKey(tableName, id string) string {
	return "cdc:processed:" + tableName + ":" + id
}

func (c *Client) eventKey(id string) string {
	return "cdc:event:" + id
}

func (c *Client) streamKey(tableName string) string {
	return "cdc:events:" + tableName
}

// ResumeToken operations
// GetResumeToken retrieves the resume token for a table
func (c *Client) GetResumeToken(ctx context.Context, tableName string) (string, error) {
	key := c.resumeTokenKey(tableName)
	token, err := c.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil // No token exists
	}
	if err != nil {
		return "", fmt.Errorf("get resume token: %w", err)
	}
	return token, nil
}

// SetResumeToken sets the resume token for a table
// Only call this after successful event processing
func (c *Client) SetResumeToken(ctx context.Context, tableName, token string) error {
	key := c.resumeTokenKey(tableName)
	return c.client.Set(ctx, key, token, 0).Err()
}

// DeleteResumeToken removes the resume token for a table
func (c *Client) DeleteResumeToken(ctx context.Context, tableName string) error {
	key := c.resumeTokenKey(tableName)
	return c.client.Del(ctx, key).Err()
}

// Idempotency operations
// IsProcessed checks if an event has already been processed
func (c *Client) IsProcessed(ctx context.Context, tableName, id string) (bool, error) {
	key := c.processedKey(tableName, id)
	exists, err := c.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("check processed: %w", err)
	}
	return exists == 1, nil
}

// MarkProcessed marks an event as processed with TTL
func (c *Client) MarkProcessed(ctx context.Context, tableName, id string, ttl time.Duration) error {
	key := c.processedKey(tableName, id)
	return c.client.Set(ctx, key, "1", ttl).Err()
}

// RetryQueue operations
// RetryEvent adds an event to the retry queue with retry count
type RetryEvent struct {
	ID          string          `json:"id"`
	TableName   string          `json:"tableName"`
	EventData   json.RawMessage `json:"eventData"` // Raw JSON of the stream record
	RetryCount  int             `json:"retryCount"`
	MaxRetries  int             `json:"maxRetries"`
	NextRetryAt time.Time       `json:"nextRetryAt"`
}

// EnqueueRetry adds an event to the retry queue with exponential backoff
func (c *Client) EnqueueRetry(ctx context.Context, event RetryEvent) error {
	key := c.retryQueueKey(event.TableName)

	// Use sorted set with nextRetryAt as score and event ID as member
	score := float64(event.NextRetryAt.UnixNano())
	return c.client.ZAdd(ctx, key, redis.Z{
		Score:  score,
		Member: event.ID,
	}).Err()
}

// StoreRetryEventData stores the retry event data separately
func (c *Client) StoreRetryEventData(ctx context.Context, tableName, id string, event RetryEvent) error {
	key := c.retryQueueKey(tableName) + ":data:" + id
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal retry event: %w", err)
	}
	return c.client.Set(ctx, key, data, 1*time.Hour).Err()
}

// GetRetryEventData retrieves the retry event data
func (c *Client) GetRetryEventData(ctx context.Context, tableName, id string) (*RetryEvent, error) {
	key := c.retryQueueKey(tableName) + ":data:" + id
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("get retry event data: %w", err)
	}
	var event RetryEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, fmt.Errorf("unmarshal retry event: %w", err)
	}
	return &event, nil
}

// RemoveRetryEventData removes the retry event data
func (c *Client) RemoveRetryEventData(ctx context.Context, tableName, id string) error {
	key := c.retryQueueKey(tableName) + ":data:" + id
	return c.client.Del(ctx, key).Err()
}

// DequeueRetry gets IDs of events ready for retry (nextRetryAt <= now)
// Events are removed from queue only after successful processing
func (c *Client) DequeueRetry(ctx context.Context, tableName string, limit int64) ([]string, error) {
	key := c.retryQueueKey(tableName)
	now := time.Now().UnixNano()

	// Get event IDs with score <= now
	ids, err := c.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min:   "0",
		Max:   fmt.Sprintf("%d", now),
		Count: limit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("dequeue retry: %w", err)
	}

	return ids, nil
}

// RemoveRetryEvent removes a processed event from the retry queue by ID
func (c *Client) RemoveRetryEvent(ctx context.Context, tableName, eventID string) error {
	key := c.retryQueueKey(tableName)
	return c.client.ZRem(ctx, key, eventID).Err()
}

// GetRetryEvent retrieves a full retry event by ID
func (c *Client) GetRetryEvent(ctx context.Context, tableName, eventID string) (*RetryEvent, error) {
	return c.GetRetryEventData(ctx, tableName, eventID)
}

// DLQ operations
// SendToDLQ adds an event to the dead letter queue
func (c *Client) SendToDLQ(ctx context.Context, tableName string, event interface{}) error {
	key := c.dlqKey(tableName)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal DLQ event: %w", err)
	}

	// Push to list (DLQ)
	return c.client.RPush(ctx, key, data).Err()
}

// GetDLQLength returns the number of events in the DLQ
func (c *Client) GetDLQLength(ctx context.Context, tableName string) (int64, error) {
	key := c.dlqKey(tableName)
	return c.client.LLen(ctx, key).Result()
}

// Event storage operations
// StoreEvent stores an event payload by ID
func (c *Client) StoreEvent(ctx context.Context, id string, event interface{}, ttl time.Duration) error {
	key := c.eventKey(id)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return c.client.Set(ctx, key, data, ttl).Err()
}

// GetEvent retrieves an event payload by ID
func (c *Client) GetEvent(ctx context.Context, id string, dest interface{}) error {
	key := c.eventKey(id)
	data, err := c.client.Get(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}
	return json.Unmarshal([]byte(data), dest)
}

// Monitor operations
// GetRetryQueueLength returns the number of events in the retry queue
func (c *Client) GetRetryQueueLength(ctx context.Context, tableName string) (int64, error) {
	key := c.retryQueueKey(tableName)
	return c.client.ZCard(ctx, key).Result()
}

// Health check
func (c *Client) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Pub/Sub operations
// PublishConfigChange publishes a table change notification
func (c *Client) PublishConfigChange(ctx context.Context, tableName string) error {
	channel := "cdc:config-change"
	return c.client.Publish(ctx, channel, tableName).Err()
}

// SubscribeConfigChanges subscribes to table change notifications
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
