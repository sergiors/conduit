package config

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var discardLogger = log.New(io.Discard, "", 0)

func TestLoadShutdownTimeout(t *testing.T) {
	t.Run("defaults to 30s when empty", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration(discardLogger, "SHUTDOWN_TIMEOUT", "", 30*time.Second))
	})

	t.Run("parses a valid duration string", func(t *testing.T) {
		assert.Equal(t, 45*time.Second, loadDuration(discardLogger, "SHUTDOWN_TIMEOUT", "45s", 30*time.Second))
		assert.Equal(t, 2*time.Minute, loadDuration(discardLogger, "SHUTDOWN_TIMEOUT", "2m", 30*time.Second))
	})

	t.Run("falls back to default on invalid value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration(discardLogger, "SHUTDOWN_TIMEOUT", "not-a-duration", 30*time.Second))
	})

	t.Run("falls back to default on zero value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration(discardLogger, "SHUTDOWN_TIMEOUT", "0s", 30*time.Second))
	})

	t.Run("falls back to default on negative value", func(t *testing.T) {
		assert.Equal(t, 30*time.Second, loadDuration(discardLogger, "SHUTDOWN_TIMEOUT", "-1s", 30*time.Second))
	})
}

func TestLoad_RequiredVariables(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("PORT", "9090")

	cfg := Load(discardLogger)

	assert.Equal(t, "mongodb://localhost:27017", cfg.MongoDBURI)
	assert.Equal(t, "conduit", cfg.MongoDBDatabase)
	assert.Equal(t, "redis://localhost:6379", cfg.RedisURI)
	assert.Equal(t, 10*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, "9090", cfg.Port)
}

func TestLoad_PortDefaultWhenUnset(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	// Clear PORT explicitly so a developer's exported value cannot leak in and
	// mask the default.
	t.Setenv("PORT", "")

	cfg := Load(discardLogger)

	assert.Equal(t, "8080", cfg.Port)
}

func TestLoad_ShutdownTimeoutDefault(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	// Clear SHUTDOWN_TIMEOUT explicitly so a developer's exported value cannot
	// leak in and mask a regression here (a set-but-invalid value would fall
	// back to the default anyway, but a valid value would not).
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	cfg := Load(discardLogger)

	assert.Equal(t, 30*time.Second, cfg.ShutdownTimeout)
}
