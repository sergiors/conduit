// Package config centralizes application configuration loaded from the
// environment. The rest of the application should obtain settings through
// Load() rather than reading environment variables directly.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds all application settings.
type Config struct {
	MongoDBURI      string
	MongoDBDatabase string
	RedisURI        string
	Port            string
	MetricsAddr     string
	ShutdownTimeout time.Duration
	// LogLevel is the slog level selected by LOG_LEVEL (default Info). It is
	// used by cmd/main.go to build the process logger after config.Load.
	LogLevel slog.Level
}

// Load reads the full application configuration from the environment; every
// conduit command uses it. It requires the three connection settings every
// command needs — MONGODB_URI, MONGODB_DATABASE and REDIS_URI — plus the
// optional PORT (default "8080"), the opt-in METRICS_ADDR (the worker's
// Prometheus /metrics listen address; metrics are disabled when unset),
// SHUTDOWN_TIMEOUT (default 30s) tuning knobs and LOG_LEVEL (default INFO).
// There is deliberately a single loader: there is no longer a reason to let
// some commands omit the broker configuration, so the whole CLI stays
// consistent about the settings it reads.
//
// An invalid LOG_LEVEL is a configuration error and aborts startup (see
// loadLogLevel): the process must fail fast rather than silently run at an
// unintended level.
func Load(logger *slog.Logger) Config {
	return Config{
		MongoDBURI:      requiredEnv(logger, "MONGODB_URI"),
		MongoDBDatabase: requiredEnv(logger, "MONGODB_DATABASE"),
		RedisURI:        requiredEnv(logger, "REDIS_URI"),
		Port:            getEnv("PORT", "8080"),
		MetricsAddr:     getEnv("METRICS_ADDR", ""),
		ShutdownTimeout: loadDuration(logger, "SHUTDOWN_TIMEOUT", getEnv("SHUTDOWN_TIMEOUT", "30s"), 30*time.Second),
		LogLevel:        loadLogLevel(logger, getEnv("LOG_LEVEL", "INFO")),
	}
}

// logLevelNames are the documented LOG_LEVEL values, in order of increasing
// verbosity, used to render a helpful error message on an invalid value.
var logLevelNames = []string{"DEBUG", "INFO", "WARN", "ERROR"}

// ParseLogLevel parses a LOG_LEVEL value into a slog.Level. It trims leading
// and trailing whitespace and is case-insensitive ("info"/"Info"/"INFO" all
// work), though the documented form is uppercase. The accepted values map
// 1:1 onto slog's built-in levels: DEBUG, INFO, WARN and ERROR. "WARNING" is
// also accepted as an alias for WARN (slog names the constant Warn, but
// operators commonly write "WARNING"). An empty value returns slog.LevelInfo
// (the default). Any other value returns an error listing the valid values so
// callers can render a clear configuration error rather than silently falling
// back.
func ParseLogLevel(value string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "":
		return slog.LevelInfo, nil
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO":
		return slog.LevelInfo, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf(
			"invalid LOG_LEVEL %q: valid values are %s",
			value, strings.Join(logLevelNames, ", "),
		)
	}
}

// loadLogLevel parses LOG_LEVEL and, on an invalid value, reports a clear
// configuration error and aborts startup. Unlike optional tuning knobs
// (loadDuration falls back), an invalid log level is a configuration error:
// silently running at an unintended level would obscure precisely the
// operational feedback the operator asked for. The valid value set is small and
// enumerated, so there is no ambiguity worth falling back on. The injected
// logger is non-nil at this entry point (the CLI owns logger creation).
func loadLogLevel(logger *slog.Logger, value string) slog.Level {
	level, err := ParseLogLevel(value)
	if err != nil {
		logger.Error("Configuration error", "error", err)
		os.Exit(1)
	}
	return level
}

// loadDuration parses an optional duration environment variable into a
// time.Duration. On an empty, invalid, or non-positive value it logs a warning
// and falls back to the provided default rather than aborting the process:
// these are optional tuning knobs, not required settings. Non-positive values
// (e.g. "0s", "-1s") are rejected because they would otherwise disable or
// invert operational behavior such as graceful shutdown.
func loadDuration(logger *slog.Logger, name, value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		logger.Warn("Invalid configuration value, using default",
			"name", name, "value", value, "default", fallback, "error", err)
		return fallback
	}
	if d <= 0 {
		logger.Warn("Invalid configuration value (must be positive), using default",
			"name", name, "value", value, "default", fallback)
		return fallback
	}
	return d
}

// requiredEnv returns the value of the environment variable key, or exits the
// process if it is empty.
//
// NOTE: this is PRE-EXISTING behavior preserved as-is. requiredEnv exits the
// process on a missing required environment variable. It is only ever reached
// from the executable boundary (the conduit CLI commands call Load), so the
// fatal exit is intentional and must not be converted to a returned error. The
// injected logger is non-nil at this entry point (the CLI owns logger
// creation). The error is logged at ERROR and the process exits non-zero.
func requiredEnv(logger *slog.Logger, key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	logger.Error("Required environment variable is not set", "name", key)
	os.Exit(1)
	return ""
}

// getEnv returns the value of the environment variable key, or defaultValue if
// it is empty.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
