package collections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerDeletionProtection(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	// Cleanup any leftover from previous runs
	if _, err := manager.Get(ctx, "protection_toggle_table"); err == nil {
		_ = manager.DisableDeletionProtection(ctx, "protection_toggle_table")
		_ = manager.Delete(ctx, "protection_toggle_table")
	}

	table := &Collection{
		CollectionName: "protection_toggle_table",
		StreamEnabled:  true,
	}
	require.NoError(t, manager.Create(ctx, table))

	t.Run("protection enabled by default on create", func(t *testing.T) {
		got, err := manager.Get(ctx, "protection_toggle_table")
		require.NoError(t, err)
		assert.True(t, got.DeletionProtection, "protection should be enabled by default")
	})

	t.Run("enable protection again is immutable", func(t *testing.T) {
		err := manager.EnableDeletionProtection(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrProtectionAlreadyExists), "should match ErrProtectionAlreadyExists")
		got, _ := manager.Get(ctx, "protection_toggle_table")
		assert.True(t, got.DeletionProtection)
	})

	t.Run("delete blocked while protected", func(t *testing.T) {
		err := manager.Delete(ctx, "protection_toggle_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrDeletionProtectionEnabled), "should match ErrDeletionProtectionEnabled")
	})

	t.Run("disable protection", func(t *testing.T) {
		require.NoError(t, manager.DisableDeletionProtection(ctx, "protection_toggle_table"))
		got, _ := manager.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection, "protection should be disabled")
	})

	t.Run("disable protection is idempotent", func(t *testing.T) {
		require.NoError(t, manager.DisableDeletionProtection(ctx, "protection_toggle_table"))
		got, _ := manager.Get(ctx, "protection_toggle_table")
		assert.False(t, got.DeletionProtection)
	})

	t.Run("enable protection again after disable", func(t *testing.T) {
		require.NoError(t, manager.EnableDeletionProtection(ctx, "protection_toggle_table"))
		got, _ := manager.Get(ctx, "protection_toggle_table")
		assert.True(t, got.DeletionProtection)
	})

	t.Run("unknown collection returns not found", func(t *testing.T) {
		err := manager.EnableDeletionProtection(ctx, "does_not_exist_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionNotFound), "should match ErrCollectionNotFound")

		err = manager.DisableDeletionProtection(ctx, "does_not_exist_table")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrCollectionNotFound), "should match ErrCollectionNotFound")
	})

	// Cleanup
	require.NoError(t, manager.DisableDeletionProtection(ctx, "protection_toggle_table"))
	require.NoError(t, manager.Delete(ctx, "protection_toggle_table"))
}
