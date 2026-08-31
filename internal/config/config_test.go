package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadShutdownTimeout(t *testing.T) {
	t.Run("defaults to 30s when empty", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadShutdownTimeout(""))
	})

	t.Run("parses a valid duration string", func(t *testing.T) {
		assert.Equal(t, 45*time.Second, loadShutdownTimeout("45s"))
		assert.Equal(t, 2*time.Minute, loadShutdownTimeout("2m"))
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadShutdownTimeout("not-a-duration"))
	})
}

func TestLoadWorker_RequiredVariables(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")

	cfg := LoadWorker()

	assert.Equal(t, "mongodb://localhost:27017", cfg.MongoDBURI)
	assert.Equal(t, "conduit", cfg.MongoDBDatabase)
	assert.Equal(t, "redis://localhost:6379", cfg.RedisURI)
	assert.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
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
