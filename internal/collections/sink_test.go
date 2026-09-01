package collections

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestSinkFingerprintDeterministic(t *testing.T) {
	// Two sinks differing only in JSON key insertion order must produce the
	// same fingerprint. EventTypes order and mutable filter/behavior are not
	// part of the identity (they are updated in place via PATCH).
	a := Sink{
		Type: SinkTypeHTTP,
		Spec: map[string]interface{}{
			"endpoint": "http://localhost:3000/events",
			"headers":  map[string]interface{}{"x-a": "1", "x-b": "2"},
		},
		EventTypes: []string{"INSERT", "MODIFY"},
		Filter: Filter{
			NewImage: ImageFilter{
				"status": FilterCondition{Eq: "active"},
			},
		},
	}
	b := Sink{
		Type: SinkTypeHTTP,
		Spec: map[string]interface{}{
			"headers":  map[string]interface{}{"x-b": "2", "x-a": "1"},
			"endpoint": "http://localhost:3000/events",
		},
		EventTypes: []string{"MODIFY", "INSERT"},
		Filter: Filter{
			NewImage: ImageFilter{
				"status": FilterCondition{Eq: "active"},
			},
		},
	}

	fpA, err := a.ComputeFingerprint()
	require.NoError(t, err)
	fpB, err := b.ComputeFingerprint()
	require.NoError(t, err)
	assert.Equal(t, fpA, fpB)
	assert.NotEmpty(t, fpA)
}

func TestSinkFingerprintIgnoresAdminFields(t *testing.T) {
	base := Sink{
		Type:       SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		EventTypes: []string{"INSERT"},
	}
	other := base
	other.ID = "different-id"
	other.CollectionID = "different-collection"
	other.CreatedAt = time.Now().Add(-time.Hour)
	other.UpdatedAt = time.Now().Add(time.Hour)

	fpBase, err := base.ComputeFingerprint()
	require.NoError(t, err)
	fpOther, err := other.ComputeFingerprint()
	require.NoError(t, err)
	assert.Equal(t, fpBase, fpOther)
}

func TestSinkFingerprintDistinct(t *testing.T) {
	base := Sink{
		Type:       SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		EventTypes: []string{"INSERT"},
	}
	fpBase, err := base.ComputeFingerprint()
	require.NoError(t, err)

	// The fingerprint covers the immutable identity (type + spec) only.
	// Mutable/behavioral fields are updated in place via PATCH and must not
	// change the destination identity.
	t.Run("different type changes fingerprint", func(t *testing.T) {
		s := base
		s.Type = SinkTypeEventBridge
		fp, err := s.ComputeFingerprint()
		require.NoError(t, err)
		assert.NotEqual(t, fpBase, fp)
	})

	t.Run("different spec value changes fingerprint", func(t *testing.T) {
		s := base
		s.Spec = map[string]interface{}{"endpoint": "http://localhost:9999/events"}
		fp, err := s.ComputeFingerprint()
		require.NoError(t, err)
		assert.NotEqual(t, fpBase, fp)
	})

	t.Run("different event types keep fingerprint", func(t *testing.T) {
		s := base
		s.EventTypes = []string{"INSERT", "MODIFY"}
		fp, err := s.ComputeFingerprint()
		require.NoError(t, err)
		assert.Equal(t, fpBase, fp)
	})

	t.Run("different filter keeps fingerprint", func(t *testing.T) {
		s := base
		s.Filter = Filter{NewImage: ImageFilter{"status": FilterCondition{Eq: "active"}}}
		fp, err := s.ComputeFingerprint()
		require.NoError(t, err)
		assert.Equal(t, fpBase, fp)
	})
}

func TestCreateSinkRejectsDuplicate(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	// Cleanup any leftover from previous runs
	if c, err := manager.Get(ctx, "sink_dup_table"); err == nil {
		if c.DeletionProtection {
			_ = manager.DisableDeletionProtection(ctx, "sink_dup_table")
		}
		_ = manager.Delete(ctx, "sink_dup_table")
	}

	table := &Collection{
		CollectionName: "sink_dup_table",
		StreamEnabled:  true,
	}
	require.NoError(t, manager.Create(ctx, table))

	// Ensure the unique fingerprint index exists.
	require.NoError(t, manager.CreateIndex(ctx))

	sink := Sink{
		Type:       SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		EventTypes: []string{"INSERT", "MODIFY"},
	}

	_, err := manager.CreateSink(ctx, "sink_dup_table", sink)
	require.NoError(t, err)

	t.Run("identical sink rejected", func(t *testing.T) {
		// Same functional identity, different EventTypes order.
		dup := sink
		dup.EventTypes = []string{"MODIFY", "INSERT"}
		_, err := manager.CreateSink(ctx, "sink_dup_table", dup)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSinkAlreadyExists)
	})

	t.Run("different sink succeeds", func(t *testing.T) {
		diff := Sink{
			Type: SinkTypeHTTP,
			Spec: map[string]interface{}{"endpoint": "http://localhost:9999/events"},
		}
		created, err := manager.CreateSink(ctx, "sink_dup_table", diff)
		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
	})

	// Cleanup
	if c, err := manager.Get(ctx, "sink_dup_table"); err == nil {
		if c.DeletionProtection {
			_ = manager.DisableDeletionProtection(ctx, "sink_dup_table")
		}
		_ = manager.Delete(ctx, "sink_dup_table")
	}
}

func TestSinkBSONTags(t *testing.T) {
	sink := Sink{
		ID:           "sink1",
		CollectionID: "coll1",
		Type:         SinkTypeHTTP,
		Spec:         map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		EventTypes:   []string{"INSERT"},
	}

	data, err := bson.Marshal(sink)
	require.NoError(t, err)

	var decoded map[string]interface{}
	err = bson.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "sink1", decoded["_id"])
	assert.Equal(t, "coll1", decoded["collectionId"])
	assert.Equal(t, string(SinkTypeHTTP), decoded["type"])
	assert.Equal(t, map[string]interface{}{"endpoint": "http://localhost:3000/events"}, decoded["spec"])
	assert.Equal(t, []interface{}{"INSERT"}, []interface{}(decoded["eventTypes"].(primitive.A)))
}

func TestManagerSinkCRUD(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	// Cleanup any leftover from previous runs
	if c, err := manager.Get(ctx, "sink_test_table"); err == nil {
		if c.DeletionProtection {
			_ = manager.DisableDeletionProtection(ctx, "sink_test_table")
		}
		_ = manager.Delete(ctx, "sink_test_table")
	}

	table := &Collection{
		CollectionName: "sink_test_table",
		StreamEnabled:  true,
	}
	require.NoError(t, manager.Create(ctx, table))

	t.Run("create sink", func(t *testing.T) {
		sink, err := manager.CreateSink(ctx, "sink_test_table", Sink{
			Type: SinkTypeHTTP,
			Spec: map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, sink.ID)
		assert.Equal(t, table.ID, sink.CollectionID)
		assert.Equal(t, SinkTypeHTTP, sink.Type)
		assert.Equal(t, map[string]interface{}{"endpoint": "http://localhost:3000/events"}, sink.Spec)
	})

	t.Run("get sinks", func(t *testing.T) {
		sinks, err := manager.GetSinks(ctx, "sink_test_table")
		require.NoError(t, err)
		assert.Len(t, sinks, 1)
	})

	t.Run("delete sink", func(t *testing.T) {
		sinks, err := manager.GetSinks(ctx, "sink_test_table")
		require.NoError(t, err)
		require.Len(t, sinks, 1)

		require.NoError(t, manager.DeleteSink(ctx, "sink_test_table", sinks[0].ID))

		sinks, err = manager.GetSinks(ctx, "sink_test_table")
		require.NoError(t, err)
		assert.Empty(t, sinks)
	})

	t.Run("delete sink not found", func(t *testing.T) {
		err := manager.DeleteSink(ctx, "sink_test_table", "000000000000000000000000")
		assert.ErrorIs(t, err, ErrSinkNotFound)
	})

	t.Run("cascade delete sinks on collection delete", func(t *testing.T) {
		_, err := manager.CreateSink(ctx, "sink_test_table", Sink{
			Type: SinkTypeHTTP,
			Spec: map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		})
		require.NoError(t, err)

		require.NoError(t, manager.DisableDeletionProtection(ctx, "sink_test_table"))
		require.NoError(t, manager.Delete(ctx, "sink_test_table"))

		count, err := client.Database("conduit_test").Collection("config.sinks").
			CountDocuments(ctx, bson.M{"collectionId": table.ID})
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})

	// Cleanup
	if c, err := manager.Get(ctx, "sink_test_table"); err == nil {
		if c.DeletionProtection {
			_ = manager.DisableDeletionProtection(ctx, "sink_test_table")
		}
		_ = manager.Delete(ctx, "sink_test_table")
	}
}

func TestManagerUpdateSinkMutableFields(t *testing.T) {
	manager, _, ctx := newTestManager(t)
	const name = "sink_update_table"

	// Cleanup leftovers from a previous run.
	if c, err := manager.Get(ctx, name); err == nil {
		if c.DeletionProtection {
			_ = manager.DisableDeletionProtection(ctx, name)
		}
		_ = manager.Delete(ctx, name)
	}

	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: name, StreamEnabled: true}))

	created, err := manager.CreateSink(ctx, name, Sink{
		Type:       SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "http://localhost:3000/events"},
		EventTypes: []string{"INSERT"},
		Filter: Filter{
			NewImage: ImageFilter{"status": FilterCondition{Eq: "active"}},
		},
	})
	require.NoError(t, err)
	fpBefore := created.Fingerprint

	// Replace OnPublish to count firings.
	rec := &hookRecorder{}
	manager.OnPublish = rec.record()

	updatedAtBefore := created.UpdatedAt

	t.Run("update mutable fields succeeds", func(t *testing.T) {
		newFilter := Filter{NewImage: ImageFilter{"status": FilterCondition{Eq: "paused"}}}
		updated, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{
			Filter:     &newFilter,
			EventTypes: []string{"INSERT", "MODIFY"},
		})
		require.NoError(t, err)
		assert.Equal(t, newFilter, updated.Filter)
		assert.Equal(t, []string{"INSERT", "MODIFY"}, updated.EventTypes)
		assert.Equal(t, fpBefore, updated.Fingerprint, "fingerprint must not change: it covers type+spec only")
		assert.True(t, updated.UpdatedAt.After(updatedAtBefore), "updatedAt should advance")
		assert.Equal(t, 1, rec.count(), "OnPublish should fire exactly once")
	})

	t.Run("update eventTypes to empty means all types", func(t *testing.T) {
		// Non-nil empty slice sets eventTypes to all types.
		updated, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{
			EventTypes: []string{},
		})
		require.NoError(t, err)
		assert.Empty(t, updated.EventTypes)
		assert.Equal(t, fpBefore, updated.Fingerprint)
	})

	t.Run("change type rejected as immutable", func(t *testing.T) {
		newType := SinkTypeEventBridge
		_, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{Type: &newType})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSinkIdentityImmutable)
	})

	t.Run("change spec rejected as immutable", func(t *testing.T) {
		newSpec := map[string]interface{}{"endpoint": "http://localhost:9999/other"}
		_, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{Spec: newSpec})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSinkIdentityImmutable)
	})

	t.Run("same type and spec allowed even when echoed", func(t *testing.T) {
		// Echoing the current identity back is a legal no-op for identity; only
		// a change is rejected.
		sameSpec := map[string]interface{}{"endpoint": "http://localhost:3000/events"}
		sameType := SinkTypeHTTP
		_, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{
			Type: &sameType,
			Spec: sameSpec,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValidation)
	})

	t.Run("unknown sink id", func(t *testing.T) {
		_, err := manager.UpdateSink(ctx, name, "000000000000000000000000", SinkUpdate{EventTypes: []string{"INSERT"}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSinkNotFound)
	})

	t.Run("invalid event type via update", func(t *testing.T) {
		_, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{
			EventTypes: []string{"INVALID"},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValidation)
	})

	t.Run("update with no mutable fields is a validation error", func(t *testing.T) {
		_, err := manager.UpdateSink(ctx, name, created.ID, SinkUpdate{})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrValidation)
	})

	// Cleanup
	if c, err := manager.Get(ctx, name); err == nil {
		if c.DeletionProtection {
			_ = manager.DisableDeletionProtection(ctx, name)
		}
		_ = manager.Delete(ctx, name)
	}
}
