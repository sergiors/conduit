package collections

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsStream(t *testing.T) {
	settings, _, ctx := newTestSettings(t)

	// Cleanup leftovers
	if _, err := settings.Get(ctx, "stream_test_table"); err == nil {
		_ = settings.DisableDeletionProtection(ctx, "stream_test_table")
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
		assert.True(t, errors.Is(err, ErrStreamAlreadyExists), "should match ErrStreamAlreadyExists")
		got, _ := settings.Get(ctx, "stream_test_table")
		assert.True(t, got.StreamEnabled)
		assert.True(t, got.OldImage)
	})

	t.Run("change old_image after enabled is immutable", func(t *testing.T) {
		err := settings.EnableStream(ctx, "stream_test_table", false)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrStreamAlreadyExists), "should match ErrStreamAlreadyExists")
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
	require.NoError(t, settings.DisableDeletionProtection(ctx, "stream_test_table"))
	require.NoError(t, settings.Delete(ctx, "stream_test_table"))
}

// TestEnableStreamEnsuresPreImageCapability is the regression test for the
// end-to-end old_image contract: when a stream is enabled with old_image, the
// physical MongoDB collection MUST have the changeStreamPreAndPostImages
// capability. Without it, MongoDB accepts the change stream but silently omits
// fullDocumentBeforeChange on update/replace/delete events — the pre-image is
// lost at the source before any Conduit code can see it.
//
// The regression target is a collection created WITHOUT the capability (as
// pre-b415252 Conduit versions and external tooling did): EnableStream must
// repair it. The test proves the observable behavior — an update to a document
// must surface fullDocumentBeforeChange on the change stream that the watcher
// opens — not just the collMod side effect.
func TestEnableStreamEnsuresPreImageCapability(t *testing.T) {
	settings, client, ctx := newTestSettings(t)

	const name = "preimage_capability_test_table"

	// Cleanup leftovers
	if _, err := settings.Get(ctx, name); err == nil {
		_ = settings.DisableDeletionProtection(ctx, name)
		_ = settings.Delete(ctx, name)
	}

	// A collection created outside Settings.Create (no pre-image capability),
	// mirroring pre-b415252 Conduit and external provisioning.
	require.NoError(t, client.Database("conduit_test").CreateCollection(ctx, name))
	t.Cleanup(func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.Database("conduit_test").Collection(name).Drop(bgCtx)
	})

	// Sanity check: the collection starts without the capability.
	assert.False(t, hasPreImageCapability(ctx, t, client, name), "precondition: fresh collection has no pre-image capability")

	// Enable the stream with old_image; EnableStream must repair the gap.
	table := &Collection{CollectionName: name, StreamEnabled: false}
	require.NoError(t, settings.Create(ctx, table))
	require.NoError(t, settings.EnableStream(ctx, name, true))
	t.Cleanup(func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = settings.DisableStream(bgCtx, name)
	})

	// The change stream is opened through the same options the watcher uses,
	// BEFORE the writes, so every operation is observed from the oplog.
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer streamCancel()

	opts := options.ChangeStream()
	opts.SetFullDocument(options.UpdateLookup)
	opts.SetFullDocumentBeforeChange(options.WhenAvailable)

	coll := client.Database("conduit_test").Collection(name)
	cursor, err := coll.Watch(streamCtx, mongo.Pipeline{}, opts)
	require.NoError(t, err)
	defer cursor.Close(streamCtx)

	writeCtx, writeCancel := context.WithTimeout(streamCtx, 15*time.Second)
	defer writeCancel()

	if _, err := coll.InsertOne(writeCtx, bson.M{"_id": "doc-1", "status": "active", "v": int32(1)}); err != nil {
		require.NoError(t, err)
	}
	if err := coll.FindOneAndReplace(writeCtx, bson.M{"_id": "doc-1"}, bson.M{"_id": "doc-1", "status": "active", "v": int32(2)}).Err(); err != nil {
		require.NoError(t, err)
	}
	if err := coll.FindOneAndUpdate(writeCtx, bson.M{"_id": "doc-1"}, bson.M{"$set": bson.M{"status": "shipped"}}).Err(); err != nil {
		require.NoError(t, err)
	}
	if _, err := coll.DeleteOne(writeCtx, bson.M{"_id": "doc-1"}); err != nil {
		require.NoError(t, err)
	}

	sawReplace := false
	sawUpdate := false
	sawDelete := false
	deadline := time.After(20 * time.Second)
	for !sawReplace || !sawUpdate || !sawDelete {
		select {
		case <-streamCtx.Done():
			t.Fatalf("change stream ended before observing all pre-images: replace=%v update=%v delete=%v (last error: %v)", sawReplace, sawUpdate, sawDelete, cursor.Err())
		case <-deadline:
			t.Fatalf("timed out observing pre-images: replace=%v update=%v delete=%v", sawReplace, sawUpdate, sawDelete)
		default:
		}
		if !cursor.Next(streamCtx) {
			continue
		}
		var change bson.M
		require.NoError(t, cursor.Decode(&change))
		switch change["operationType"] {
		case "replace":
			assert.NotNil(t, change["fullDocumentBeforeChange"], "replace must carry a pre-image")
			sawReplace = hasPreImage(change)
		case "update":
			assert.NotNil(t, change["fullDocumentBeforeChange"], "update must carry a pre-image")
			sawUpdate = hasPreImage(change)
		case "delete":
			// Delete events may arrive before the earlier replace/update events
			// are surfaced from the oplog in rare orderings; only assert
			// pre-image presence when the expected document is observed.
			if dk, ok := change["documentKey"].(bson.M); ok && dk["_id"] == "doc-1" {
				assert.NotNil(t, change["fullDocumentBeforeChange"], "delete must carry a pre-image")
				sawDelete = hasPreImage(change)
			}
		}
	}
}

func hasPreImage(change bson.M) bool {
	doc, ok := change["fullDocumentBeforeChange"].(bson.M)
	return ok && doc != nil
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
