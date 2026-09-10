// Package config centralizes application configuration loaded from the
// environment. The rest of the application should obtain settings through
// Load() rather than reading environment variables directly.
package config

import (
	"io"
	"log"
	"os"
	"time"
)

// Config holds all application settings.
type Config struct {
	MongoDBURI      string
	MongoDBDatabase string
	RedisURI        string
	Port            string
	APIKey          string
	ShutdownTimeout time.Duration
}

// Load is the API binary's loader. It reads the full application
// configuration from the environment, hard-requiring API_KEY because the API's
// bearer-token auth middleware depends on fail-closed behavior. Worker binaries
// should use LoadWorker instead, which requires only what the worker consumes.
func Load(logger *log.Logger) Config {
	return Config{
		MongoDBURI:      requiredEnv(logger, "MONGODB_URI"),
		MongoDBDatabase: requiredEnv(logger, "MONGODB_DATABASE"),
		RedisURI:        requiredEnv(logger, "REDIS_URI"),
		Port:            getEnv("PORT", "8080"),
		APIKey:          requiredEnv(logger, "API_KEY"),
	}
}

// LoadWorker reads the worker process's configuration from the environment.
//
// The worker connects to MongoDB and Redis and never serves HTTP nor holds the
// API auth secret, so API_KEY and PORT are intentionally not read from the
// environment: a worker-only deployment must not need the API's credential.
// Only the settings the worker consumes are populated; Port and APIKey are left
// at their zero values.
func LoadWorker(logger *log.Logger) Config {
	return Config{
		MongoDBURI:      requiredEnv(logger, "MONGODB_URI"),
		MongoDBDatabase: requiredEnv(logger, "MONGODB_DATABASE"),
		RedisURI:        requiredEnv(logger, "REDIS_URI"),
		ShutdownTimeout: loadDuration(logger, "SHUTDOWN_TIMEOUT", getEnv("SHUTDOWN_TIMEOUT", "30s"), 30*time.Second),
	}
}

// loadDuration parses an optional duration environment variable into a
// time.Duration. On an empty, invalid, or non-positive value it logs a warning
// and falls back to the provided default rather than aborting the process:
// these are optional tuning knobs, not required settings. Non-positive values
// (e.g. "0s", "-1s") are rejected because they would otherwise disable or
// invert operational behavior such as graceful shutdown.
func loadDuration(logger *log.Logger, name, value string, fallback time.Duration) time.Duration {
	logger = nilGuard(logger)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		logger.Printf("Invalid %s %q, using default %s: %v", name, value, fallback, err)
		return fallback
	}
	if d <= 0 {
		logger.Printf("Invalid %s %q (must be positive), using default %s", name, value, fallback)
		return fallback
	}
	return d
}

// requiredEnv returns the value of the environment variable key, or exits the
// process if it is empty.
//
// NOTE: this is PRE-EXISTING behavior preserved as-is. requiredEnv calls
// logger.Fatalf (process exit) on a missing required environment variable. It
// is only ever reached from the executable boundary (cmd/api and cmd/worker
// call Load/LoadWorker), so the fatal exit is intentional and must not be
// converted to a returned error. The injected logger is nil-guarded so a nil
// logger still exits the process rather than panicking.
func requiredEnv(logger *log.Logger, key string) string {
	logger = nilGuard(logger)
	if value := os.Getenv(key); value != "" {
		return value
	}
	logger.Fatalf("Required environment variable %s is not set", key)
	return ""
}

// nilGuard returns a discard logger when logger is nil so a nil *log.Logger
// never panics. It is the uniform nil-logger policy across the codebase.
func nilGuard(logger *log.Logger) *log.Logger {
	if logger == nil {
		return log.New(io.Discard, "", 0)
	}
	return logger
}

// getEnv returns the value of the environment variable key, or defaultValue if
// it is empty.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
