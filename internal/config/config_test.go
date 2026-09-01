package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadShutdownTimeout(t *testing.T) {
	t.Run("defaults to 30s when empty", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration("SHUTDOWN_TIMEOUT", "", 30*time.Second))
	})

	t.Run("parses a valid duration string", func(t *testing.T) {
		assert.Equal(t, 45*time.Second, loadDuration("SHUTDOWN_TIMEOUT", "45s", 30*time.Second))
		assert.Equal(t, 2*time.Minute, loadDuration("SHUTDOWN_TIMEOUT", "2m", 30*time.Second))
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration("SHUTDOWN_TIMEOUT", "not-a-duration", 30*time.Second))
	})

	t.Run("falls back to default on zero value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration("SHUTDOWN_TIMEOUT", "0s", 30*time.Second))
	})

	t.Run("falls back to default on negative value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration("SHUTDOWN_TIMEOUT", "-1s", 30*time.Second))
	})
}

func TestLoadProcessedEventTTL(t *testing.T) {
	t.Run("defaults to 24h when empty", func(t *testing.T) {
		assert.Equal(t, 24*time.Hour, loadDuration("PROCESSED_EVENT_TTL", "", 24*time.Hour))
	})

	t.Run("parses a valid duration string", func(t *testing.T) {
		assert.Equal(t, 12*time.Hour, loadDuration("PROCESSED_EVENT_TTL", "12h", 24*time.Hour))
		assert.Equal(t, 48*time.Hour, loadDuration("PROCESSED_EVENT_TTL", "48h", 24*time.Hour))
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		assert.Equal(t, 24*time.Hour, loadDuration("PROCESSED_EVENT_TTL", "not-a-duration", 24*time.Hour))
	})

	t.Run("falls back to default on zero value", func(t *testing.T) {
		assert.Equal(t, 24*time.Hour, loadDuration("PROCESSED_EVENT_TTL", "0s", 24*time.Hour))
	})

	t.Run("falls back to default on negative value", func(t *testing.T) {
		assert.Equal(t, 24*time.Hour, loadDuration("PROCESSED_EVENT_TTL", "-1s", 24*time.Hour))
	})
}

func TestLoadWorker_RequiredVariables(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("PROCESSED_EVENT_TTL", "12h")

	cfg := LoadWorker()

	assert.Equal(t, "mongodb://localhost:27017", cfg.MongoDBURI)
	assert.Equal(t, "conduit", cfg.MongoDBDatabase)
	assert.Equal(t, "redis://localhost:6379", cfg.RedisURI)
	assert.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, 12*time.Hour, cfg.ProcessedEventTTL)
	// The worker must not depend on the API's credential nor serve HTTP.
	assert.Equal(t, "", cfg.APIKey)
	assert.Equal(t, "", cfg.Port)
}

func TestLoadWorker_ShutdownTimeoutDefault(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	// Clear SHUTDOWN_TIMEOUT explicitly so a developer's exported value cannot
	// leak in and mask a regression here (a set-but-invalid value would fall
	// back to the default anyway, but a valid value would not).
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg := LoadWorker()

	assert.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}

func TestLoadWorker_ProcessedEventTTLDefault(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	// Clear PROCESSED_EVENT_TTL explicitly so a developer's exported value
	// cannot leak in and mask a regression here.
	t.Setenv("PROCESSED_EVENT_TTL", "")

	cfg := LoadWorker()

	assert.Equal(t, 24*time.Hour, cfg.ProcessedEventTTL)
}
