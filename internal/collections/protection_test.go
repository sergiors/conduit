package collections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsSetDeletionProtection(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	// Cleanup any leftover from previous runs
	if tbl, err := settings.Get(ctx, "protection_toggle_table"); err == nil {
		if tbl.DeletionProtection {
			_ = settings.SetDeletionProtection(ctx, "protection_toggle_table", false)
		}
		_ = settings.Delete(ctx, "protection_toggle_table")
	}

	table := &Collection{
		CollectionName:     "protection_toggle_table",
		StreamEnabled:      true,
		DeletionProtection: false,
	}
	require.NoError(t, settings.Create(ctx, table))

	t.Run("enable protection", func(t *testing.T) {
		require.NoError(t, settings.SetDeletionProtection(ctx, "protection_toggle_table", true))
		got, err := settings.Get(ctx, "protection_toggle_table")
		require.NoError(t, err)
		assert.True(t, got.DeletionProtection, "protection should be enabled")
	})

	t.Run("enable is idempotent", func(t *testing.T) {
		require.NoError(t, settings.SetDeletionProtection(ctx, "protection_toggle_table", true))
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.True(t, got.DeletionProtection)
	})

	t.Run("delete blocked while protected", func(t *testing.T) {
		err := settings.Delete(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deletion protection")
	})

	t.Run("disable protection", func(t *testing.T) {
		require.NoError(t, settings.SetDeletionProtection(ctx, "protection_toggle_table", false))
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection, "protection should be disabled")
	})

	t.Run("disable is idempotent", func(t *testing.T) {
		require.NoError(t, settings.SetDeletionProtection(ctx, "protection_toggle_table", false))
		got, _ := settings.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection)
	})

	t.Run("unknown collection returns not found", func(t *testing.T) {
		err := settings.SetDeletionProtection(ctx, "does_not_exist_table", true)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "collection not found")
		assert.True(t, errors.Is(err, ErrCollectionNotFound), "should match ErrCollectionNotFound")
	})

	t.Run("delete protected returns ErrDeletionProtectionEnabled", func(t *testing.T) {
		require.NoError(t, settings.SetDeletionProtection(ctx, "protection_toggle_table", true))
		err := settings.Delete(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrDeletionProtectionEnabled), "should match ErrDeletionProtectionEnabled")
		// Restore to unprotected so the cleanup below can delete it.
		require.NoError(t, settings.SetDeletionProtection(ctx, "protection_toggle_table", false))
	})

	// Cleanup
	require.NoError(t, settings.Delete(ctx, "protection_toggle_table"))
}
