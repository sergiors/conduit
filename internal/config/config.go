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
		ShutdownTimeout: loadShutdownTimeout(getEnv("SHUTDOWN_TIMEOUT", "30s")),
	}
}

// loadShutdownTimeout parses the SHUTDOWN_TIMEOUT value into a duration. It
// bounds the whole graceful-shutdown sequence. On an invalid value it logs a
// warning and falls back to the default rather than aborting the process: this
// is an optional tuning knob, not a required setting.
func loadShutdownTimeout(value string) time.Duration {
	const fallback = 30 * time.Second
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("Invalid SHUTDOWN_TIMEOUT %q, using default %s: %v", value, fallback, err)
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
