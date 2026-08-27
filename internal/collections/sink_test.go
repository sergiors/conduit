package collections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

func TestSinkBSONTags(t *testing.T) {
	sink := Sink{
		ID:           "sink1",
		CollectionID: "coll1",
		SinkConfig: SinkConfig{
			Type:     "http",
			Endpoint: "http://localhost:3000/events",
		},
	}

	data, err := bson.Marshal(sink)
	require.NoError(t, err)

	var decoded map[string]interface{}
	err = bson.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "sink1", decoded["_id"])
	assert.Equal(t, "coll1", decoded["collection_id"])
	assert.Equal(t, "http", decoded["type"])
	assert.Equal(t, "http://localhost:3000/events", decoded["endpoint"])
}

func TestSettingsSinkCRUD(t *testing.T) {
	settings, client, ctx := newTestSettings(t)

	// Cleanup any leftover from previous runs
	if c, err := settings.Get(ctx, "sink_test_table"); err == nil {
		if c.DeletionProtection {
			_ = settings.DisableDeletionProtection(ctx, "sink_test_table")
		}
		_ = settings.Delete(ctx, "sink_test_table")
	}

	table := &Collection{
		CollectionName: "sink_test_table",
		StreamEnabled:  true,
	}
	require.NoError(t, settings.Create(ctx, table))

	t.Run("create sink", func(t *testing.T) {
		sink, err := settings.CreateSink(ctx, "sink_test_table", SinkConfig{
			Type:     "http",
			Endpoint: "http://localhost:3000/events",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, sink.ID)
		assert.Equal(t, table.ID, sink.CollectionID)
		assert.Equal(t, "http", sink.Type)
		assert.Equal(t, "http://localhost:3000/events", sink.Endpoint)
	})

	t.Run("get sinks", func(t *testing.T) {
		sinks, err := settings.GetSinks(ctx, "sink_test_table")
		require.NoError(t, err)
		assert.Len(t, sinks, 1)
	})

	t.Run("delete sink", func(t *testing.T) {
		sinks, err := settings.GetSinks(ctx, "sink_test_table")
		require.NoError(t, err)
		require.Len(t, sinks, 1)

		require.NoError(t, settings.DeleteSink(ctx, "sink_test_table", sinks[0].ID))

		sinks, err = settings.GetSinks(ctx, "sink_test_table")
		require.NoError(t, err)
		assert.Empty(t, sinks)
	})

	t.Run("delete sink not found", func(t *testing.T) {
		err := settings.DeleteSink(ctx, "sink_test_table", "000000000000000000000000")
		assert.ErrorIs(t, err, ErrSinkNotFound)
	})

	t.Run("cascade delete sinks on collection delete", func(t *testing.T) {
		_, err := settings.CreateSink(ctx, "sink_test_table", SinkConfig{
			Type:     "http",
			Endpoint: "http://localhost:3000/events",
		})
		require.NoError(t, err)

		require.NoError(t, settings.DisableDeletionProtection(ctx, "sink_test_table"))
		require.NoError(t, settings.Delete(ctx, "sink_test_table"))

		count, err := client.Database("conduit_test").Collection("config.sinks").
			CountDocuments(ctx, bson.M{"collection_id": table.ID})
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})

	// Cleanup
	if c, err := settings.Get(ctx, "sink_test_table"); err == nil {
		if c.DeletionProtection {
			_ = settings.DisableDeletionProtection(ctx, "sink_test_table")
		}
		_ = settings.Delete(ctx, "sink_test_table")
	}
}
