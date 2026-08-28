// Package config centralizes application configuration loaded from the
// environment. The rest of the application should obtain settings through
// Load() rather than reading environment variables directly.
package config

import (
	"log"
	"os"
)

// Config holds all application settings.
type Config struct {
	MongoDBURI      string
	MongoDBDatabase string
	RedisURI        string
	Port            string
	APIKey          string
}

// Load reads the application configuration from the environment.
func Load() Config {
	return Config{
		MongoDBURI:      requiredEnv("MONGODB_URI"),
		MongoDBDatabase: requiredEnv("MONGODB_DATABASE"),
		RedisURI:        requiredEnv("REDIS_URI"),
		Port:            getEnv("PORT", "8080"),
		APIKey:          requiredEnv("API_KEY"),
	}
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
