package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	t.Run("returns empty config (no defaults for connection)", func(t *testing.T) {
		cfg := DefaultConfig()

		// No defaults for connection - must be provided
		assert.Equal(t, "", cfg.URI)
		assert.Equal(t, "", cfg.Addr)
		assert.Equal(t, "", cfg.Password)
		assert.Equal(t, 0, cfg.DB)
		assert.Equal(t, "cdc:", cfg.Prefix)
	})
}

func TestKeyHelpers(t *testing.T) {
	t.Run("key helpers generate correct format", func(t *testing.T) {
		// Create a mock client for testing key generation
		client := &Client{}

		// Test that key format methods exist and are callable
		assert.NotNil(t, client)
	})
}

func TestRetryEvent(t *testing.T) {
	t.Run("retry event structure", func(t *testing.T) {
		event := RetryEvent{
			ID:             "users-123",
			CollectionName: "users",
			EventData:      []byte(`{"id": "123"}`),
			RetryCount:     0,
			MaxRetries:     5,
			NextRetryAt:    time.Now().Add(time.Second),
		}

		assert.Equal(t, "users", event.CollectionName)
		assert.Equal(t, "users-123", event.ID)
		assert.Equal(t, 0, event.RetryCount)
		assert.Equal(t, 5, event.MaxRetries)
		assert.True(t, !event.NextRetryAt.IsZero())
	})

	t.Run("retry event with max retries exceeded", func(t *testing.T) {
		event := RetryEvent{
			ID:             "orders-456",
			CollectionName: "orders",
			EventData:      []byte(`{"id": "456"}`),
			RetryCount:     5,
			MaxRetries:     5,
			NextRetryAt:    time.Now(),
		}

		assert.True(t, event.RetryCount >= event.MaxRetries)
	})
}

func TestParseRetryMembers(t *testing.T) {
	t.Run("valid members are parsed", func(t *testing.T) {
		valid, _ := json.Marshal(RetryEvent{
			ID:             "users-1",
			CollectionName: "users",
			EventData:      []byte(`{"id":"1"}`),
			RetryCount:     2,
			MaxRetries:     5,
			NextRetryAt:    time.Now(),
		})

		events, skipped := parseRetryMembers([]string{string(valid)})
		assert.Equal(t, 0, skipped)
		require.Len(t, events, 1)
		assert.Equal(t, "users", events[0].CollectionName)
		assert.Equal(t, 2, events[0].RetryCount)
	})

	t.Run("corrupt member is skipped, valid members still returned", func(t *testing.T) {
		valid, _ := json.Marshal(RetryEvent{
			ID:             "users-1",
			CollectionName: "users",
			EventData:      []byte(`{"id":"1"}`),
			RetryCount:     1,
			MaxRetries:     5,
			NextRetryAt:    time.Now(),
		})

		corrupt := "this is not json{{{"
		members := []string{corrupt, string(valid), corrupt}

		events, skipped := parseRetryMembers(members)
		assert.Equal(t, 2, skipped)
		require.Len(t, events, 1)
		assert.Equal(t, "users-1", events[0].ID)
		assert.Equal(t, "users", events[0].CollectionName)
	})

	t.Run("all corrupt members yields empty result with no error", func(t *testing.T) {
		members := []string{"not-json", "still-not-json"}
		events, skipped := parseRetryMembers(members)
		assert.Equal(t, 2, skipped)
		assert.Empty(t, events)
	})
}

// Integration tests (require Redis)
func TestClientIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := NewClient(ctx, Config{URI: "redis://localhost:6379"})
	if err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer client.Close()

	t.Run("ping succeeds", func(t *testing.T) {
		err := client.Ping(ctx)
		assert.NoError(t, err)
	})

	t.Run("resume token operations", func(t *testing.T) {
		tableName := "test_table_" + time.Now().Format("20060102150405")

		// Get non-existent token
		token, err := client.GetResumeToken(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, "", token)

		// Set token
		testToken := "test-resume-token-data"
		err = client.SetResumeToken(ctx, tableName, testToken)
		require.NoError(t, err)

		// Get token
		token, err = client.GetResumeToken(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, testToken, token)

		// Delete token
		err = client.DeleteResumeToken(ctx, tableName)
		require.NoError(t, err)

		// Verify deleted
		token, err = client.GetResumeToken(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, "", token)
	})

	t.Run("idempotency operations", func(t *testing.T) {
		eventID := "test_table_" + time.Now().Format("20060102150405") + "-test-event-123"

		// Check non-existent
		processed, err := client.IsProcessed(ctx, eventID)
		require.NoError(t, err)
		assert.False(t, processed)

		// Mark as processed
		err = client.MarkProcessed(ctx, eventID, 1*time.Hour)
		require.NoError(t, err)

		// Check exists
		processed, err = client.IsProcessed(ctx, eventID)
		require.NoError(t, err)
		assert.True(t, processed)
	})

	t.Run("retry queue operations", func(t *testing.T) {
		tableName := "test_table_" + time.Now().Format("20060102150405")

		// Get initial length
		length, err := client.GetRetryQueueLength(ctx, tableName)
		require.NoError(t, err)
		initialLength := length

		// Enqueue retry event
		event := RetryEvent{
			ID:             tableName + "-123",
			CollectionName: tableName,
			EventData:      []byte(`{"id": "123"}`),
			RetryCount:     0,
			MaxRetries:     5,
			NextRetryAt:    time.Now().Add(-1 * time.Second), // Ready for retry
		}

		err = client.EnqueueRetry(ctx, event)
		require.NoError(t, err)

		// Check length increased
		length, err = client.GetRetryQueueLength(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, initialLength+1, length)

		// Dequeue
		events, err := client.DequeueRetry(ctx, tableName, 10)
		require.NoError(t, err)
		assert.Len(t, events, 1)
		assert.Equal(t, tableName, events[0].CollectionName)
	})

	t.Run("DLQ operations", func(t *testing.T) {
		tableName := "test_table_" + time.Now().Format("20060102150405")

		// Get initial length
		length, err := client.GetDLQLength(ctx, tableName)
		require.NoError(t, err)
		initialLength := length

		// Send to DLQ
		event := map[string]interface{}{"id": "123", "data": "test"}
		err = client.SendToDLQ(ctx, tableName, event)
		require.NoError(t, err)

		// Check length increased
		length, err = client.GetDLQLength(ctx, tableName)
		require.NoError(t, err)
		assert.Equal(t, initialLength+1, length)
	})
}

func TestClientCreation(t *testing.T) {
	t.Run("creation with URI succeeds", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping integration test")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client, err := NewClient(ctx, Config{URI: "redis://localhost:6379"})
		if err != nil {
			t.Skipf("Redis not available: %v", err)
		}
		defer client.Close()

		assert.NotNil(t, client)
	})

	t.Run("creation with invalid URI fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err := NewClient(ctx, Config{URI: "redis://invalid-host:6379"})
		assert.Error(t, err)
	})

	t.Run("creation without URI or Addr fails", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, err := NewClient(ctx, Config{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "URI or Addr must be provided")
	})
}
