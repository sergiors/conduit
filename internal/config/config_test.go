package config

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// TestParseLogLevel pins down the LOG_LEVEL value → slog.Level mapping: the
// four documented uppercase values, case-insensitivity, whitespace trimming,
// the WARNING alias for WARN, the empty default of INFO, and the clear error
// on anything else. It tests the pure helper only — the caller (Load) exits on
// the error path, which is why the invalid case is asserted here rather than by
// invoking a process-exiting Load.
func TestParseLogLevel(t *testing.T) {
	t.Run("maps documented values", func(t *testing.T) {
		cases := []struct {
			value string
			want  slog.Level
		}{
			{"DEBUG", slog.LevelDebug},
			{"INFO", slog.LevelInfo},
			{"WARN", slog.LevelWarn},
			{"ERROR", slog.LevelError},
		}
		for _, tc := range cases {
			got, err := ParseLogLevel(tc.value)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got, "value %q", tc.value)
		}
	})

	t.Run("is case-insensitive", func(t *testing.T) {
		for _, v := range []string{"info", "Info", "DEBUG", "WaRn", "error"} {
			_, err := ParseLogLevel(v)
			assert.NoError(t, err, "value %q", v)
		}
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		got, err := ParseLogLevel("  DEBUG  ")
		assert.NoError(t, err)
		assert.Equal(t, slog.LevelDebug, got)
	})

	t.Run("accepts WARNING as an alias for WARN", func(t *testing.T) {
		got, err := ParseLogLevel("WARNING")
		assert.NoError(t, err)
		assert.Equal(t, slog.LevelWarn, got)
	})

	t.Run("empty defaults to INFO", func(t *testing.T) {
		for _, v := range []string{"", "   "} {
			got, err := ParseLogLevel(v)
			assert.NoError(t, err, "value %q", v)
			assert.Equal(t, slog.LevelInfo, got, "value %q", v)
		}
	})

	t.Run("rejects unknown values with a helpful error", func(t *testing.T) {
		for _, v := range []string{"TRACE", "notice", "1", "debug2"} {
			_, err := ParseLogLevel(v)
			assert.Error(t, err)
			assert.ErrorContains(t, err, "DEBUG, INFO, WARN, ERROR", "value %q", v)
		}
	})
}

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

func TestLoad_MetricsAddrDisabledWhenUnset(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	// Clear METRICS_ADDR explicitly so a developer's exported value cannot leak
	// in and mask the default.
	t.Setenv("METRICS_ADDR", "")

	cfg := Load(discardLogger)

	// Metrics are opt-in: unset METRICS_ADDR disables them.
	assert.Equal(t, "", cfg.MetricsAddr)
}

func TestLoad_LogLevelDefault(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	// Clear LOG_LEVEL explicitly so a developer's exported value cannot leak in
	// and mask the default.
	t.Setenv("LOG_LEVEL", "")

	cfg := Load(discardLogger)

	assert.Equal(t, slog.LevelInfo, cfg.LogLevel, "unset LOG_LEVEL must default to INFO")
}

func TestLoad_LogLevelOverride(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	t.Setenv("LOG_LEVEL", "DEBUG")

	cfg := Load(discardLogger)

	assert.Equal(t, slog.LevelDebug, cfg.LogLevel, "LOG_LEVEL=DEBUG must be reflected in Config")
}

func TestLoad_MetricsAddrOverride(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://localhost:27017")
	t.Setenv("MONGODB_DATABASE", "conduit")
	t.Setenv("REDIS_URI", "redis://localhost:6379")
	t.Setenv("METRICS_ADDR", ":9100")

	cfg := Load(discardLogger)

	assert.Equal(t, ":9100", cfg.MetricsAddr)
}
