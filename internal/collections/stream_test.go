package collections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsStream(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	// Cleanup leftovers
	if _, err := settings.Get(ctx, "stream_test_table"); err == nil {
		_ = settings.SetDeletionProtection(ctx, "stream_test_table", false)
		_ = settings.Delete(ctx, "stream_test_table")
	}

	table := &Collection{
		CollectionName: "stream_test_table",
		StreamEnabled:  false,
	}
	require.NoError(t, settings.Create(ctx, table))

	t.Run("enable stream with old_image", func(t *testing.T) {
		require.NoError(t, settings.EnableStream(ctx, "stream_test_table", true))
		got, err := settings.Get(ctx, "stream_test_table")
		require.NoError(t, err)
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
	})

	t.Run("re-enable stream with same old_image is immutable", func(t *testing.T) {
		err := settings.EnableStream(ctx, "stream_test_table", true)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrOldImageImmutable), "should match ErrOldImageImmutable")
		got, _ := settings.Get(ctx, "stream_test_table")
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
	})

	t.Run("change old_image after enabled is immutable", func(t *testing.T) {
		err := settings.EnableStream(ctx, "stream_test_table", false)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrOldImageImmutable), "should match ErrOldImageImmutable")
		got, _ := settings.Get(ctx, "stream_test_table")
		assert.True(t, got.OldImage, "old_image should remain unchanged")
	})

	t.Run("disable stream resets both and allows redefinition", func(t *testing.T) {
		require.NoError(t, settings.DisableStream(ctx, "stream_test_table"))
		got, _ := settings.Get(ctx, "stream_test_table")
		assert.False(t, got.StreamEnabled)
		assert.False(t, got.OldImage)

		require.NoError(t, settings.EnableStream(ctx, "stream_test_table", false))
		got, _ = settings.Get(ctx, "stream_test_table")
		assert.True(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
	})

	t.Run("disable stream is idempotent", func(t *testing.T) {
		require.NoError(t, settings.DisableStream(ctx, "stream_test_table"))
		got, _ := settings.Get(ctx, "stream_test_table")
		assert.False(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
	})

	t.Run("stream on unknown collection returns not found", func(t *testing.T) {
		assert.True(t, errors.Is(settings.EnableStream(ctx, "does_not_exist", true), ErrCollectionNotFound))
		assert.True(t, errors.Is(settings.DisableStream(ctx, "does_not_exist"), ErrCollectionNotFound))
	})

	// Cleanup
	require.NoError(t, settings.Delete(ctx, "stream_test_table"))
}

func TestSettingsListStreamEnabled(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	// Create stream-enabled table
	streamTable := &Collection{
		CollectionName: "stream_table",
		StreamEnabled:  true,
	}
	_ = settings.Create(ctx, streamTable)

	// Create non-stream table
	nonStreamTable := &Collection{
		CollectionName: "no_stream_table",
		StreamEnabled:  false,
	}
	_ = settings.Create(ctx, nonStreamTable)

	tables, err := settings.ListStreamEnabled(ctx)
	require.NoError(t, err)

	found := false
	for _, table := range tables {
		if table.CollectionName == "stream_table" {
			found = true
			assert.True(t, table.StreamEnabled)
		}
		if table.CollectionName == "no_stream_table" {
			assert.Fail(t, "non-stream table should not be in list")
		}
	}
	assert.True(t, found, "stream_table should be in the list")

	// Cleanup
	_ = settings.Delete(ctx, streamTable.CollectionName)
	_ = settings.Delete(ctx, nonStreamTable.CollectionName)
}
