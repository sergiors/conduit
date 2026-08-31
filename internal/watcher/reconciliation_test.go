package watcher

import (
	"context"
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	_ "github.com/sergiors/conduit/internal/dispatch/transports" // register transport builders
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rawSpec(endpoint string) map[string]interface{} {
	return map[string]interface{}{"endpoint": endpoint}
}

func TestReconcileSinks(t *testing.T) {
	t.Run("no changes when sinks are identical", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://webhook.example.com")},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://webhook.example.com")},
		}

		rec := ReconcileSinks(current, desired)
		assert.Empty(t, rec.Changes)
		assert.Equal(t, "0 added, 0 removed, 0 updated", rec.Summary())
	})

	t.Run("add new sink", func(t *testing.T) {
		current := []collections.Sink{}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://new.example.com")},
		}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 1)
		assert.Equal(t, ChangeAdded, rec.Changes[0].Type)
		assert.Equal(t, "s1", rec.Changes[0].Sink.ID)
		assert.Equal(t, "1 added, 0 removed, 0 updated", rec.Summary())
	})

	t.Run("remove sink", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://remove.example.com")},
		}
		desired := []collections.Sink{}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 1)
		assert.Equal(t, ChangeRemoved, rec.Changes[0].Type)
		assert.Equal(t, "s1", rec.Changes[0].Sink.ID)
		assert.Equal(t, "0 added, 1 removed, 0 updated", rec.Summary())
	})

	t.Run("mutable config change emits update", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://example.com"), EventTypes: []string{"INSERT"}},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://example.com"), EventTypes: []string{"INSERT", "MODIFY"}},
		}

		rec := ReconcileSinks(current, desired)
		require.Len(t, rec.Changes, 1)
		assert.Equal(t, ChangeUpdated, rec.Changes[0].Type)
		assert.Equal(t, "s1", rec.Changes[0].Sink.ID)
		assert.Equal(t, "0 added, 0 removed, 1 updated", rec.Summary())
	})

	t.Run("mutable filter change emits update", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://example.com"), Filter: collections.Filter{NewImage: collections.ImageFilter{"status": collections.FilterCondition{Eq: "active"}}}},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://example.com"), Filter: collections.Filter{NewImage: collections.ImageFilter{"status": collections.FilterCondition{Eq: "paused"}}}},
		}

		rec := ReconcileSinks(current, desired)
		require.Len(t, rec.Changes, 1)
		assert.Equal(t, ChangeUpdated, rec.Changes[0].Type)
		assert.Equal(t, "0 added, 0 removed, 1 updated", rec.Summary())
	})

	t.Run("mixed add and remove", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://keep.example.com")},
			{ID: "s2", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://remove.example.com")},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://keep.example.com")},
			{ID: "s3", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://add.example.com")},
		}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 2)

		changes := make(map[ChangeType]int)
		ids := make(map[string]bool)
		for _, c := range rec.Changes {
			changes[c.Type]++
			ids[c.Sink.ID] = true
		}
		assert.Equal(t, 1, changes[ChangeAdded])
		assert.Equal(t, 1, changes[ChangeRemoved])
		assert.True(t, ids["s2"])
		assert.True(t, ids["s3"])
		assert.Equal(t, "1 added, 1 removed, 0 updated", rec.Summary())
	})

	t.Run("remove all sinks", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://a.example.com")},
			{ID: "s2", Type: collections.SinkTypeHTTP, Spec: rawSpec("https://b.example.com")},
		}
		desired := []collections.Sink{}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 2)
		assert.Equal(t, ChangeRemoved, rec.Changes[0].Type)
		assert.Equal(t, ChangeRemoved, rec.Changes[1].Type)
		assert.Equal(t, "0 added, 2 removed, 0 updated", rec.Summary())
	})
}

func TestReconciliationLogChanges(t *testing.T) {
	t.Run("log added changes", func(t *testing.T) {
		rec := &Reconciliation{
			Changes: []SinkChange{
				{Type: ChangeAdded, Sink: collections.Sink{ID: "s1"}},
			},
		}
		// Just verify it doesn't panic
		rec.LogChanges("users")
	})

	t.Run("log removed changes", func(t *testing.T) {
		rec := &Reconciliation{
			Changes: []SinkChange{
				{Type: ChangeRemoved, Sink: collections.Sink{ID: "s1"}},
			},
		}
		rec.LogChanges("users")
	})

	t.Run("log updated changes", func(t *testing.T) {
		rec := &Reconciliation{
			Changes: []SinkChange{
				{Type: ChangeUpdated, Sink: collections.Sink{ID: "s1"}},
			},
		}
		rec.LogChanges("users")
	})
}

func TestReconciliationApplyChangesUpdate(t *testing.T) {
	ctx := context.Background()
	disp := &mockDispatcher{sinks: make(map[string][]string)}

	// Simulate a runtime sink that was registered earlier and then PATCHed:
	// ApplyChanges must route the update to the dispatcher's Update (in-place
	// config swap) and must NOT build a transport or re-register.
	update := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeHTTP,
		Spec:       rawSpec("https://example.com"),
		EventTypes: []string{"MODIFY"},
	}
	rec := &Reconciliation{
		Changes: []SinkChange{
			{Type: ChangeUpdated, Sink: update},
		},
	}

	rec.ApplyChanges(ctx, "users", disp)

	require.Len(t, disp.updated["users"], 1)
	assert.Equal(t, update, disp.updated["users"][0], "Update should receive the desired sink config")
	assert.Empty(t, disp.sinks["users"], "update must not re-register the sink")
	assert.Zero(t, disp.buildTransportCalls, "update must not build a transport")
}

func TestReconciliationApplyChangesUpdateFallbackRegister(t *testing.T) {
	ctx := context.Background()
	falseResult := false
	disp := &mockDispatcher{
		sinks:        make(map[string][]string),
		updateResult: &falseResult,
	}

	// An updated sink that is not currently registered (e.g. created while the
	// watcher was stopped) must fall back to a full register so the update is
	// not lost.
	update := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeHTTP,
		Spec:       rawSpec("https://example.com"),
		EventTypes: []string{"MODIFY"},
	}
	rec := &Reconciliation{
		Changes: []SinkChange{
			{Type: ChangeUpdated, Sink: update},
		},
	}

	rec.ApplyChanges(ctx, "users", disp)

	require.Len(t, disp.updated["users"], 1, "Update should still be attempted first")
	assert.Equal(t, []string{"s1"}, disp.sinks["users"], "fallback should register the sink")
	assert.Equal(t, 1, disp.buildTransportCalls, "fallback should build a transport once")
}

// mockDispatcher is a test double for the dispatcher interface used by
// Reconciliation.ApplyChanges. It records the operations it receives so tests
// can assert that updates route to Update (not Remove+Register) and that
// BuildTransport is not invoked for updates.
type mockDispatcher struct {
	sinks   map[string][]string
	updated map[string][]collections.Sink
	// updateResult controls what Update returns; defaults to true.
	updateResult *bool
	// buildTransportCalls counts how many times a transport would be built
	// (i.e. how many times Register was reached via the added/fallback path).
	buildTransportCalls int
}

func (m *mockDispatcher) Register(collection string, sink *dispatch.RuntimeSink) {
	if sink != nil {
		m.sinks[collection] = append(m.sinks[collection], sink.Key())
	}
	m.buildTransportCalls++
}

func (m *mockDispatcher) Remove(collection, id string) {
	sinks := m.sinks[collection]
	for i, d := range sinks {
		if d == id {
			m.sinks[collection] = append(sinks[:i], sinks[i+1:]...)
			return
		}
	}
}

func (m *mockDispatcher) Update(collection string, sink collections.Sink) bool {
	if m.updated == nil {
		m.updated = make(map[string][]collections.Sink)
	}
	m.updated[collection] = append(m.updated[collection], sink)
	if m.updateResult != nil {
		return *m.updateResult
	}
	return true
}
