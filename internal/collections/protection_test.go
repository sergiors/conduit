package collections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsDeletionProtection(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	// Cleanup any leftover from previous runs
	if _, err := settings.Get(ctx, "protection_toggle_table"); err == nil {
		_ = settings.DisableDeletionProtection(ctx, "protection_toggle_table")
		_ = settings.Delete(ctx, "protection_toggle_table")
	}

	table := &Collection{
		CollectionName: "protection_toggle_table",
		StreamEnabled:  true,
	}
	require.NoError(t, settings.Create(ctx, table))

	t.Run("protection enabled by default on create", func(t *testing.T) {
		got, err := settings.Get(ctx, "protection_toggle_table")
		require.NoError(t, err)
		assert.True(t, got.DeletionProtection, "protection should be enabled by default")
	})

	t.Run("enable protection again is immutable", func(t *testing.T) {
		err := settings.EnableDeletionProtection(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrProtectionAlreadyExists), "should match ErrProtectionAlreadyExists")
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.True(t, got.DeletionProtection)
	})

	t.Run("delete blocked while protected", func(t *testing.T) {
		err := settings.Delete(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrDeletionProtectionEnabled), "should match ErrDeletionProtectionEnabled")
	})

	t.Run("disable protection", func(t *testing.T) {
		require.NoError(t, settings.DisableDeletionProtection(ctx, "protection_toggle_table"))
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection, "protection should be disabled")
	})

	t.Run("disable protection is idempotent", func(t *testing.T) {
		require.NoError(t, settings.DisableDeletionProtection(ctx, "protection_toggle_table"))
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection)
	})

	t.Run("enable protection again after disable", func(t *testing.T) {
		require.NoError(t, settings.EnableDeletionProtection(ctx, "protection_toggle_table"))
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.True(t, got.DeletionProtection)
	})

	t.Run("unknown collection returns not found", func(t *testing.T) {
		err := settings.EnableDeletionProtection(ctx, "does_not_exist_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionNotFound), "should match ErrCollectionNotFound")

		err = settings.DisableDeletionProtection(ctx, "does_not_exist_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionNotFound), "should match ErrCollectionNotFound")
	})

	// Cleanup
	require.NoError(t, settings.DisableDeletionProtection(ctx, "protection_toggle_table"))
	require.NoError(t, settings.Delete(ctx, "protection_toggle_table"))
}
