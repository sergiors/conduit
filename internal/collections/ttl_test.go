package collections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsTTL(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	// Cleanup leftovers
	if _, err := settings.Get(ctx, "ttl_test_table"); err == nil {
		_ = settings.DisableTTL(ctx, "ttl_test_table")
		_ = settings.DisableDeletionProtection(ctx, "ttl_test_table")
		_ = settings.Delete(ctx, "ttl_test_table")
	}

	table := &Collection{
		CollectionName: "ttl_test_table",
		StreamEnabled:  false,
	}
	require.NoError(t, settings.Create(ctx, table))

	t.Run("enable ttl sets attribute", func(t *testing.T) {
		require.NoError(t, settings.SetTTL(ctx, "ttl_test_table", "expiresAt"))
		got, _ := settings.Get(ctx, "ttl_test_table")
		assert.Equal(t, "expiresAt", got.TTLAttribute)
	})

	t.Run("re-enable ttl with same attribute is immutable", func(t *testing.T) {
		err := settings.SetTTL(ctx, "ttl_test_table", "expiresAt")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrTTLAlreadyExists), "should match ErrTTLAlreadyExists")
		got, _ := settings.Get(ctx, "ttl_test_table")
		assert.Equal(t, "expiresAt", got.TTLAttribute)
	})

	t.Run("enable ttl different attribute is immutable", func(t *testing.T) {
		err := settings.SetTTL(ctx, "ttl_test_table", "ttl")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrTTLAlreadyExists), "should match ErrTTLAlreadyExists")
		// Attribute is unchanged
		got, _ := settings.Get(ctx, "ttl_test_table")
		assert.Equal(t, "expiresAt", got.TTLAttribute)
	})

	t.Run("enable ttl empty attribute is validation error", func(t *testing.T) {
		err := settings.SetTTL(ctx, "ttl_test_table", "")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrValidation), "should match ErrValidation")
	})

	t.Run("disable ttl clears attribute", func(t *testing.T) {
		require.NoError(t, settings.DisableTTL(ctx, "ttl_test_table"))
		got, _ := settings.Get(ctx, "ttl_test_table")
		assert.Equal(t, "", got.TTLAttribute)
	})

	t.Run("disable ttl idempotent", func(t *testing.T) {
		require.NoError(t, settings.DisableTTL(ctx, "ttl_test_table"))
	})

	t.Run("ttl on unknown collection returns not found", func(t *testing.T) {
		assert.True(t, errors.Is(settings.SetTTL(ctx, "does_not_exist", "expiresAt"), ErrCollectionNotFound))
		err := settings.DisableTTL(ctx, "does_not_exist")
		assert.True(t, errors.Is(err, ErrCollectionNotFound))
	})

	// Cleanup
	require.NoError(t, settings.DisableDeletionProtection(ctx, "ttl_test_table"))
	require.NoError(t, settings.Delete(ctx, "ttl_test_table"))
}
