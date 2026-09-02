// Package config centralizes application configuration loaded from the
// environment. The rest of the application should obtain settings through
// Load() rather than reading environment variables directly.
package config

import (
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
func Load() Config {
	return Config{
		MongoDBURI:      requiredEnv("MONGODB_URI"),
		MongoDBDatabase: requiredEnv("MONGODB_DATABASE"),
		RedisURI:        requiredEnv("REDIS_URI"),
		Port:            getEnv("PORT", "8080"),
		APIKey:          requiredEnv("API_KEY"),
	}
}

// LoadWorker reads the worker process's configuration from the environment.
//
// The worker connects to MongoDB and Redis and never serves HTTP nor holds the
// API auth secret, so API_KEY and PORT are intentionally not read from the
// environment: a worker-only deployment must not need the API's credential.
// Only the settings the worker consumes are populated; Port and APIKey are left
// at their zero values.
func LoadWorker() Config {
	return Config{
		MongoDBURI:      requiredEnv("MONGODB_URI"),
		MongoDBDatabase: requiredEnv("MONGODB_DATABASE"),
		RedisURI:        requiredEnv("REDIS_URI"),
		ShutdownTimeout: loadDuration("SHUTDOWN_TIMEOUT", getEnv("SHUTDOWN_TIMEOUT", "30s"), 30*time.Second),
	}
}

// loadDuration parses an optional duration environment variable into a
// time.Duration. On an empty, invalid, or non-positive value it logs a warning
// and falls back to the provided default rather than aborting the process:
// these are optional tuning knobs, not required settings. Non-positive values
// (e.g. "0s", "-1s") are rejected because they would otherwise disable or
// invert operational behavior such as graceful shutdown.
func loadDuration(name, value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("Invalid %s %q, using default %s: %v", name, value, fallback, err)
		return fallback
	}
	if d <= 0 {
		log.Printf("Invalid %s %q (must be positive), using default %s", name, value, fallback)
		return fallback
	}
	return d
}

// requiredEnv returns the value of the environment variable key, or exits the
// process if it is empty.
func requiredEnv(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	log.Fatalf("Required environment variable %s is not set", key)
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
