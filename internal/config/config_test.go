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
