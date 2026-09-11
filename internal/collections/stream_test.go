package collections

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerStream(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	// Cleanup leftovers
	if _, err := manager.Get(ctx, "stream_test_table"); err == nil {
		_ = manager.DisableDeletionProtection(ctx, "stream_test_table")
		_ = manager.Delete(ctx, "stream_test_table")
	}

	table := &Collection{
		CollectionName: "stream_test_table",
		StreamEnabled:  false,
	}
	require.NoError(t, manager.Create(ctx, table))

	t.Run("enable stream with oldImage", func(t *testing.T) {
		require.NoError(t, manager.EnableStream(ctx, "stream_test_table", true))
		got, err := manager.Get(ctx, "stream_test_table")
		require.NoError(t, err)
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
		// The first-start checkpoint must be captured at enablement and be a
		// recent timestamp (within a minute of now).
		require.NotNil(t, got.StreamStartedAt, "EnableStream must record streamStartedAt")
		assert.InDelta(t, time.Now().Unix(), got.StreamStartedAt.T, 60, "checkpoint must be near the enablement time")
	})

	t.Run("re-enable stream with same oldImage is immutable", func(t *testing.T) {
		err := manager.EnableStream(ctx, "stream_test_table", true)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrStreamAlreadyExists), "should match ErrStreamAlreadyExists")
		got, _ := manager.Get(ctx, "stream_test_table")
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
	})

	t.Run("change oldImage after enabled is immutable", func(t *testing.T) {
		err := manager.EnableStream(ctx, "stream_test_table", false)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrStreamAlreadyExists), "should match ErrStreamAlreadyExists")
		got, _ := manager.Get(ctx, "stream_test_table")
		assert.True(t, got.OldImage, "oldImage should remain unchanged")
	})

	t.Run("disable stream resets both and allows redefinition", func(t *testing.T) {
		require.NoError(t, manager.DisableStream(ctx, "stream_test_table"))
		got, _ := manager.Get(ctx, "stream_test_table")
		assert.False(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
		// Disabling must clear the first-start checkpoint.
		assert.Nil(t, got.StreamStartedAt, "DisableStream must unset streamStartedAt")

		require.NoError(t, manager.EnableStream(ctx, "stream_test_table", false))
		got, _ = manager.Get(ctx, "stream_test_table")
		assert.True(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
		// Re-enable captures a fresh checkpoint.
		require.NotNil(t, got.StreamStartedAt, "re-enable must capture a fresh checkpoint")
	})

	t.Run("disable stream is idempotent", func(t *testing.T) {
		require.NoError(t, manager.DisableStream(ctx, "stream_test_table"))
		got, _ := manager.Get(ctx, "stream_test_table")
		assert.False(t, got.StreamEnabled)
		assert.False(t, got.OldImage)
		assert.Nil(t, got.StreamStartedAt, "idempotent disable keeps the checkpoint cleared")
	})

	t.Run("stream on unknown collection returns not found", func(t *testing.T) {
		assert.True(t, errors.Is(manager.EnableStream(ctx, "does_not_exist", true), ErrCollectionNotFound))
		assert.True(t, errors.Is(manager.DisableStream(ctx, "does_not_exist"), ErrCollectionNotFound))
	})

	// Cleanup
	require.NoError(t, manager.DisableDeletionProtection(ctx, "stream_test_table"))
	require.NoError(t, manager.Delete(ctx, "stream_test_table"))
}

// TestEnableStreamRejectsMissingPreImageCapability codifies the oldImage
// verify-not-repair contract: when a stream is enabled with oldImage on a
// physical collection that does NOT have the changeStreamPreAndPostImages
// capability (e.g. one created outside Conduit), EnableStream must reject the
// enablement with a validation error and must NOT persist the stream-enabled
// state. Enabling the same collection without oldImage (no capability check)
// remains valid.
func TestEnableStreamRejectsMissingPreImageCapability(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	const name = "preimage_capability_test_table"

	// Cleanup leftovers
	if _, err := manager.Get(ctx, name); err == nil {
		_ = manager.DisableDeletionProtection(ctx, name)
		_ = manager.Delete(ctx, name)
	}

	// A collection created outside Manager.Create (no pre-image capability),
	// mirroring external provisioning. Manager.Create refuses to adopt a
	// collection that already physically exists (ErrCollectionAlreadyExists),
	// so insert the config document directly.
	require.NoError(t, client.Database("conduit_test").CreateCollection(ctx, name))
	t.Cleanup(func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Database("conduit_test").Collection(name).Drop(bgCtx)
	})

	// Sanity check: the collection starts without the capability.
	assert.False(t, hasPreImageCapability(ctx, t, client, name), "precondition: fresh collection has no pre-image capability")

	cfg := &Collection{
		CollectionName:     name,
		StreamEnabled:      false,
		DeletionProtection: true,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	_, err := manager.collection.InsertOne(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = manager.collection.DeleteOne(bgCtx, bson.M{"collectionName": name})
	})

	// Enabling with oldImage fails with a validation error.
	err = manager.EnableStream(ctx, name, true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidation), "missing pre-image capability should surface as a validation error")

	// The rejected enablement must NOT persist the stream-enabled state.
	got, err := manager.Get(ctx, name)
	require.NoError(t, err)
	assert.False(t, got.StreamEnabled, "stream must not be enabled after rejected oldImage enablement")
	assert.False(t, got.OldImage, "oldImage must not be set after rejected oldImage enablement")
	assert.Nil(t, got.StreamStartedAt, "streamStartedAt must not be set after rejected oldImage enablement")

	// Preserved behavior: the same collection can still be enabled WITHOUT
	// oldImage — no capability check, no MongoDB-level verification.
	require.NoError(t, manager.EnableStream(ctx, name, false))
	t.Cleanup(func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = manager.DisableStream(bgCtx, name)
	})
	got, err = manager.Get(ctx, name)
	require.NoError(t, err)
	assert.True(t, got.StreamEnabled, "enablement without oldImage must succeed")
	assert.False(t, got.OldImage)
}

// TestEnableStreamAlreadyEnabledTakesPrecedenceOverCapability is a regression
// test for the immutability guard in EnableStream. A re-enable attempt on an
// already-enabled stream is an immutability violation (ErrStreamAlreadyExists)
// and must be surfaced even when the physical collection has lost the
// changeStreamPreAndPostImages capability — which would otherwise produce a 400
// validation error. Immutability (409) must take precedence over capability
// validation (400).
func TestEnableStreamAlreadyEnabledTakesPrecedenceOverCapability(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	const name = "precedence_capability_test_table"

	// Cleanup leftovers
	if _, err := manager.Get(ctx, name); err == nil {
		_ = manager.DisableDeletionProtection(ctx, name)
		_ = manager.Delete(ctx, name)
	}

	// A Conduit-created collection carries the changeStreamPreAndPostImages
	// capability, so enabling with oldImage works.
	table := &Collection{
		CollectionName: name,
		StreamEnabled:  false,
	}
	require.NoError(t, manager.Create(ctx, table))
	require.NoError(t, manager.EnableStream(ctx, name, true))

	got, err := manager.Get(ctx, name)
	require.NoError(t, err)
	assert.True(t, got.StreamEnabled)
	assert.True(t, got.OldImage)

	// Simulate capability loss on the physical collection: collMod disables
	// changeStreamPreAndPostImages on the already-created collection. The
	// config document still has streamEnabled=true; only the physical
	// capability went away.
	cmd := bson.D{
		{Key: "collMod", Value: name},
		{Key: "changeStreamPreAndPostImages", Value: bson.M{"enabled": false}},
	}
	require.NoError(t, client.Database("conduit_test").RunCommand(ctx, cmd).Err())

	// A re-enable attempt with oldImage=true must surface the immutability
	// error — NOT a validation error — even though the physical capability is
	// gone.
	err = manager.EnableStream(ctx, name, true)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrStreamAlreadyExists),
		"re-enable with oldImage must return ErrStreamAlreadyExists (immutability precedes capability), got: %v", err)

	// Immutability is independent of oldImage: re-enabling with oldImage=false
	// must also return ErrStreamAlreadyExists.
	err = manager.EnableStream(ctx, name, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrStreamAlreadyExists),
		"re-enable without oldImage must return ErrStreamAlreadyExists, got: %v", err)

	// Cleanup: disable the stream (idempotent), then drop the collection.
	require.NoError(t, manager.DisableStream(ctx, name))
	manager.DisableDeletionProtection(ctx, name)
	manager.Delete(ctx, name)
}

// hasPreImageCapability reads the changeStreamPreAndPostImages setting from
// listCollections, the authoritative MongoDB view of the capability.
func hasPreImageCapability(ctx context.Context, t *testing.T, client *mongo.Client, name string) bool {
	t.Helper()
	specs, err := client.Database("conduit_test").ListCollectionSpecifications(ctx, bson.M{"name": name})
	require.NoError(t, err)
	require.Len(t, specs, 1, "collection %s should exist", name)

	var opts struct {
		ChangeStreamPreAndPostImages struct {
			Enabled bool `bson:"enabled"`
		} `bson:"changeStreamPreAndPostImages"`
	}
	if len(specs[0].Options) == 0 {
		return false
	}
	require.NoError(t, bson.Unmarshal(specs[0].Options, &opts))
	return opts.ChangeStreamPreAndPostImages.Enabled
}

func TestManagerListStreamEnabled(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	// Create stream-enabled table
	streamTable := &Collection{
		CollectionName: "stream_table",
		StreamEnabled:  true,
	}
	_ = manager.Create(ctx, streamTable)

	// Create non-stream table
	nonStreamTable := &Collection{
		CollectionName: "no_stream_table",
		StreamEnabled:  false,
	}
	_ = manager.Create(ctx, nonStreamTable)

	tables, err := manager.ListStreamEnabled(ctx)
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
	_ = manager.Delete(ctx, streamTable.CollectionName)
	_ = manager.Delete(ctx, nonStreamTable.CollectionName)
}
