package cli

import (
	"bytes"
	"context"
	"testing"

	"conduit/internal/mongo"
	"conduit/internal/redis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// healthEnv sets the three env vars required by config.Load. Tests calling
// runHealth must set these so requiredEnv does not abort the process.
func healthEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit_test")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
}

// stubProbes overrides the mongoProbe/redisProbe package seams for the duration
// of the test and returns capture addresses holding the configs each probe
// received.
func stubProbes(t *testing.T, mongoStatus, redisStatus string) (*mongo.Config, *redis.Config) {
	t.Helper()
	mc := &mongo.Config{}
	rc := &redis.Config{}

	om := mongoProbe
	or := redisProbe
	mongoProbe = func(_ context.Context, cfg mongo.Config) string {
		*mc = cfg
		return mongoStatus
	}
	redisProbe = func(_ context.Context, cfg redis.Config) string {
		*rc = cfg
		return redisStatus
	}
	t.Cleanup(func() {
		mongoProbe = om
		redisProbe = or
	})
	return mc, rc
}

func TestRunHealth_BothHealthy(t *testing.T) {
	healthEnv(t)
	buf := &bytes.Buffer{}
	_, _ = stubProbes(t, "healthy", "healthy")

	err := runHealth(context.Background(), buf, discardLogger)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "MongoDB")
	assert.Contains(t, out, "Redis")
	assert.Contains(t, out, "healthy")
}

func TestRunHealth_MongoUnhealthy(t *testing.T) {
	healthEnv(t)
	buf := &bytes.Buffer{}
	mc, rc := stubProbes(t, "connection refused", "healthy")

	err := runHealth(context.Background(), buf, discardLogger)
	require.Error(t, err)
	// Both rows are still printed even when MongoDB failed.
	assert.ErrorContains(t, err, "MongoDB")
	assert.ErrorContains(t, err, "connection refused")
	out := buf.String()
	assert.Contains(t, out, "MongoDB")
	assert.Contains(t, out, "connection refused")
	assert.Contains(t, out, "Redis")
	assert.Contains(t, out, "healthy")

	assert.Equal(t, "mongodb://localhost:27017", mc.URI)
	assert.Equal(t, "conduit_test", mc.Database)
	assert.Equal(t, "redis://localhost:6379", rc.URI)
	assert.Equal(t, "cdc:", rc.Prefix)
}

func TestRunHealth_BothUnhealthy(t *testing.T) {
	healthEnv(t)
	buf := &bytes.Buffer{}
	stubProbes(t, "mongo down", "redis down")

	err := runHealth(context.Background(), buf, discardLogger)
	require.Error(t, err)
	assert.ErrorContains(t, err, "MongoDB")
	assert.ErrorContains(t, err, "mongo down")
	assert.ErrorContains(t, err, "Redis")
	assert.ErrorContains(t, err, "redis down")
}

func TestRunHealth_RedisUnhealthy(t *testing.T) {
	healthEnv(t)
	buf := &bytes.Buffer{}
	stubProbes(t, "healthy", "connection refused")

	err := runHealth(context.Background(), buf, discardLogger)
	require.Error(t, err)
	assert.ErrorContains(t, err, "Redis")
	assert.ErrorContains(t, err, "connection refused")

	out := buf.String()
	assert.Contains(t, out, "Redis")
	assert.Contains(t, out, "connection refused")
	assert.Contains(t, out, "MongoDB")
	assert.Contains(t, out, "healthy")
}
