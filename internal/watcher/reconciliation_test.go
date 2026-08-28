package watcher

import (
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/stretchr/testify/assert"
)

func rawConfig(endpoint string) map[string]interface{} {
	return map[string]interface{}{"endpoint": endpoint}
}

func TestReconcileSinks(t *testing.T) {
	t.Run("no changes when sinks are identical", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://webhook.example.com")},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://webhook.example.com")},
		}

		rec := ReconcileSinks(current, desired)
		assert.Empty(t, rec.Changes)
		assert.Equal(t, "0 added, 0 removed", rec.Summary())
	})

	t.Run("add new sink", func(t *testing.T) {
		current := []collections.Sink{}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://new.example.com")},
		}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 1)
		assert.Equal(t, ChangeAdded, rec.Changes[0].Type)
		assert.Equal(t, "s1", rec.Changes[0].Sink.ID)
		assert.Equal(t, "1 added, 0 removed", rec.Summary())
	})

	t.Run("remove sink", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://remove.example.com")},
		}
		desired := []collections.Sink{}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 1)
		assert.Equal(t, ChangeRemoved, rec.Changes[0].Type)
		assert.Equal(t, "s1", rec.Changes[0].Sink.ID)
		assert.Equal(t, "0 added, 1 removed", rec.Summary())
	})

	t.Run("configuration changes are ignored", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://old.example.com"), EventTypes: []string{"INSERT"}},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://new.example.com"), EventTypes: []string{"INSERT", "MODIFY"}},
		}

		rec := ReconcileSinks(current, desired)
		assert.Empty(t, rec.Changes)
		assert.Equal(t, "0 added, 0 removed", rec.Summary())
	})

	t.Run("mixed add and remove", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://keep.example.com")},
			{ID: "s2", Type: collections.SinkTypeHTTP, Config: rawConfig("https://remove.example.com")},
		}
		desired := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://keep.example.com")},
			{ID: "s3", Type: collections.SinkTypeHTTP, Config: rawConfig("https://add.example.com")},
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
		assert.Equal(t, "1 added, 1 removed", rec.Summary())
	})

	t.Run("remove all sinks", func(t *testing.T) {
		current := []collections.Sink{
			{ID: "s1", Type: collections.SinkTypeHTTP, Config: rawConfig("https://a.example.com")},
			{ID: "s2", Type: collections.SinkTypeHTTP, Config: rawConfig("https://b.example.com")},
		}
		desired := []collections.Sink{}

		rec := ReconcileSinks(current, desired)
		assert.Len(t, rec.Changes, 2)
		assert.Equal(t, ChangeRemoved, rec.Changes[0].Type)
		assert.Equal(t, ChangeRemoved, rec.Changes[1].Type)
		assert.Equal(t, "0 added, 2 removed", rec.Summary())
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
}

// mockDispatcher is a test double for the dispatcher interface used by
// Reconciliation.ApplyChanges.
type mockDispatcher struct {
	sinks map[string][]string
}

func (m *mockDispatcher) Register(collection string, sink *dispatch.RuntimeSink) {
	if sink != nil {
		m.sinks[collection] = append(m.sinks[collection], sink.Key())
	}
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
