package retry

import (
	"context"
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

func TestDefaultConfig(t *testing.T) {
	t.Run("returns sensible defaults", func(t *testing.T) {
		cfg := DefaultConfig()

		assert.Equal(t, 5*time.Second, cfg.Interval)
		assert.Equal(t, 5, cfg.MaxRetries)
		assert.Equal(t, 1*time.Second, cfg.BaseDelay)
		assert.Equal(t, 5*time.Minute, cfg.MaxDelay)
	})
}

func TestProcessorCreation(t *testing.T) {
	t.Run("new processor with correct configuration", func(t *testing.T) {
		cfg := DefaultConfig()
		processor := NewProcessor(nil, nil, cfg)

		assert.NotNil(t, processor)
		assert.Equal(t, cfg.Interval, processor.interval)
		assert.Equal(t, cfg.MaxRetries, processor.maxRetries)
	})
}

func TestCalculateNextRetry(t *testing.T) {
	t.Run("exponential backoff calculation", func(t *testing.T) {
		processor := &Processor{
			baseDelay: 1 * time.Second,
			maxDelay:  5 * time.Minute,
		}

		// First retry: 1s * 2^0 = 1s
		next := processor.calculateNextRetry(1)
		assert.InDelta(t, time.Now().Add(1*time.Second).Unix(), next.Unix(), 1)

		// Second retry: 1s * 2^1 = 2s
		next = processor.calculateNextRetry(2)
		assert.InDelta(t, time.Now().Add(2*time.Second).Unix(), next.Unix(), 1)

		// Third retry: 1s * 2^2 = 4s
		next = processor.calculateNextRetry(3)
		assert.InDelta(t, time.Now().Add(4*time.Second).Unix(), next.Unix(), 1)

		// Fourth retry: 1s * 2^3 = 8s
		next = processor.calculateNextRetry(4)
		assert.InDelta(t, time.Now().Add(8*time.Second).Unix(), next.Unix(), 1)
	})

	t.Run("max delay cap", func(t *testing.T) {
		processor := &Processor{
			baseDelay: 1 * time.Second,
			maxDelay:  5 * time.Minute,
		}

		// Retry 20: would be 1s * 2^19 = ~524288s, but capped at 5m
		next := processor.calculateNextRetry(20)
		assert.InDelta(t, time.Now().Add(5*time.Minute).Unix(), next.Unix(), 1)
	})
}

func TestProcessRetryEvent(t *testing.T) {
	t.Run("successful dispatch skips retry", func(t *testing.T) {
		dispatcher := dispatch.NewDispatcher()
		// Register a mock destination that always succeeds
		dispatcher.Register("users", &successDestination{})

		processor := NewProcessor(nil, dispatcher, DefaultConfig())

		ctx := context.Background()
		eventData, _ := bson.MarshalExtJSON(streams.StreamRecord{
			TableName:  "users",
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"_id": "123"},
		}, false, false)

		event := redis.RetryEvent{
			ID:             "users-123",
			CollectionName: "users",
			EventData:      eventData,
			RetryCount:     0,
			MaxRetries:     5,
		}

		// Should succeed without panic
		processor.processRetryEvent(ctx, "users", event)
	})

	t.Run("max retries exceeded skips DLQ when redis is nil", func(t *testing.T) {
		dispatcher := dispatch.NewDispatcher()
		processor := NewProcessor(nil, dispatcher, DefaultConfig())

		ctx := context.Background()
		eventData, _ := bson.MarshalExtJSON(streams.StreamRecord{
			TableName:  "users",
			RecordType: streams.InsertRecord,
		}, false, false)

		event := redis.RetryEvent{
			ID:             "users-456",
			CollectionName: "users",
			EventData:      eventData,
			RetryCount:     5, // Already at max
			MaxRetries:     5,
		}

		// Should not panic with nil redis client
		// The SendToDLQ will fail silently (logged) but not panic
		processor.processRetryEvent(ctx, "users", event)
	})
}

func TestRetryEventStructure(t *testing.T) {
	t.Run("retry event has all required fields", func(t *testing.T) {
		event := redis.RetryEvent{
			ID:             "orders-789",
			CollectionName: "orders",
			EventData:      []byte(`{}`),
			RetryCount:     2,
			MaxRetries:     5,
			NextRetryAt:    time.Now(),
		}

		assert.Equal(t, "orders", event.CollectionName)
		assert.Equal(t, "orders-789", event.ID)
		assert.Equal(t, 2, event.RetryCount)
		assert.Equal(t, 5, event.MaxRetries)
		assert.True(t, !event.NextRetryAt.IsZero())
	})
}

// successDestination is a mock destination that always succeeds
type successDestination struct{}

func (s *successDestination) Name() string { return "success" }
func (s *successDestination) Close() error { return nil }
func (s *successDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	return nil
}
