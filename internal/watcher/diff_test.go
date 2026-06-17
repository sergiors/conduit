package watcher

import (
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/stretchr/testify/assert"
)

func TestDiffSinks(t *testing.T) {
	t.Run("no changes when configs are identical", func(t *testing.T) {
		current := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://webhook.example.com"},
		}
		desired := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://webhook.example.com"},
		}

		diff := DiffSinks(current, desired)
		assert.Empty(t, diff.Changes)
		assert.Equal(t, "0 added, 0 removed, 0 updated", diff.Summary())
	})

	t.Run("add new sink", func(t *testing.T) {
		current := []collections.SinkConfig{}
		desired := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://new.example.com"},
		}

		diff := DiffSinks(current, desired)
		assert.Len(t, diff.Changes, 1)
		assert.Equal(t, ChangeAdd, diff.Changes[0].Type)
		assert.Equal(t, "https://new.example.com", diff.Changes[0].Name)
		assert.Equal(t, "1 added, 0 removed, 0 updated", diff.Summary())
	})

	t.Run("remove sink", func(t *testing.T) {
		current := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://remove.example.com"},
		}
		desired := []collections.SinkConfig{}

		diff := DiffSinks(current, desired)
		assert.Len(t, diff.Changes, 1)
		assert.Equal(t, ChangeRemove, diff.Changes[0].Type)
		assert.Equal(t, "https://remove.example.com", diff.Changes[0].Name)
		assert.Equal(t, "0 added, 1 removed, 0 updated", diff.Summary())
	})

	t.Run("update sink with changed event types", func(t *testing.T) {
		current := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://webhook.example.com", EventTypes: []string{"INSERT"}},
		}
		desired := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://webhook.example.com", EventTypes: []string{"INSERT", "MODIFY"}},
		}

		diff := DiffSinks(current, desired)
		assert.Len(t, diff.Changes, 1)
		assert.Equal(t, ChangeUpdate, diff.Changes[0].Type)
		assert.Equal(t, "https://webhook.example.com", diff.Changes[0].Name)
		assert.Contains(t, diff.Changes[0].ChangeDesc, "event-types")
		assert.Equal(t, "0 added, 0 removed, 1 updated", diff.Summary())
	})

	t.Run("update sink with changed filter criteria", func(t *testing.T) {
		current := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://webhook.example.com"},
		}
		desired := []collections.SinkConfig{
			{
				Type:     "http",
				Endpoint: "https://webhook.example.com",
				FilterCriteria: collections.FilterCriteria{
					OldImage: collections.ImageFilter{
						"status": collections.FilterCondition{
							Prefix: ptr("active"),
						},
					},
				},
			},
		}

		diff := DiffSinks(current, desired)
		assert.Len(t, diff.Changes, 1)
		assert.Equal(t, ChangeUpdate, diff.Changes[0].Type)
		assert.Contains(t, diff.Changes[0].ChangeDesc, "filter")
	})

	t.Run("mixed add, remove and update", func(t *testing.T) {
		current := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://keep.example.com"},
			{Type: "http", Endpoint: "https://remove.example.com"},
			{Type: "http", Endpoint: "https://update.example.com", EventTypes: []string{"INSERT"}},
		}
		desired := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://keep.example.com"},
			{Type: "http", Endpoint: "https://add.example.com"},
			{Type: "http", Endpoint: "https://update.example.com", EventTypes: []string{"INSERT", "MODIFY"}},
		}

		diff := DiffSinks(current, desired)
		assert.Len(t, diff.Changes, 3)

		changes := make(map[ChangeType]int)
		for _, c := range diff.Changes {
			changes[c.Type]++
		}
		assert.Equal(t, 1, changes[ChangeAdd])
		assert.Equal(t, 1, changes[ChangeRemove])
		assert.Equal(t, 1, changes[ChangeUpdate])
		assert.Equal(t, "1 added, 1 removed, 1 updated", diff.Summary())
	})

	t.Run("remove all sinks", func(t *testing.T) {
		current := []collections.SinkConfig{
			{Type: "http", Endpoint: "https://a.example.com"},
			{Type: "http", Endpoint: "https://b.example.com"},
		}
		desired := []collections.SinkConfig{}

		diff := DiffSinks(current, desired)
		assert.Len(t, diff.Changes, 2)
		assert.Equal(t, ChangeRemove, diff.Changes[0].Type)
		assert.Equal(t, ChangeRemove, diff.Changes[1].Type)
		assert.Equal(t, "0 added, 2 removed, 0 updated", diff.Summary())
	})
}

func TestDescribeChange(t *testing.T) {
	t.Run("event types change", func(t *testing.T) {
		old := collections.SinkConfig{
			Type:       "http",
			Endpoint:   "https://webhook.example.com",
			EventTypes: []string{"INSERT"},
		}
		new := collections.SinkConfig{
			Type:       "http",
			Endpoint:   "https://webhook.example.com",
			EventTypes: []string{"INSERT", "MODIFY"},
		}
		desc := describeChange(old, new)
		assert.Contains(t, desc, "event-types")
	})

	t.Run("filter criteria change", func(t *testing.T) {
		old := collections.SinkConfig{
			Type:     "http",
			Endpoint: "https://webhook.example.com",
		}
		new := collections.SinkConfig{
			Type:     "http",
			Endpoint: "https://webhook.example.com",
			FilterCriteria: collections.FilterCriteria{
				OldImage: collections.ImageFilter{
					"status": collections.FilterCondition{
						Prefix: ptr("active"),
					},
				},
			},
		}
		desc := describeChange(old, new)
		assert.Contains(t, desc, "filter")
	})

	t.Run("endpoint change", func(t *testing.T) {
		old := collections.SinkConfig{
			Type:     "http",
			Endpoint: "https://old.example.com",
		}
		new := collections.SinkConfig{
			Type:     "http",
			Endpoint: "https://new.example.com",
		}
		desc := describeChange(old, new)
		assert.Contains(t, desc, "endpoint")
	})

	t.Run("multiple changes", func(t *testing.T) {
		old := collections.SinkConfig{
			Type:       "http",
			Endpoint:   "https://old.example.com",
			EventTypes: []string{"INSERT"},
		}
		new := collections.SinkConfig{
			Type:       "http",
			Endpoint:   "https://new.example.com",
			EventTypes: []string{"INSERT", "MODIFY"},
		}
		desc := describeChange(old, new)
		assert.Contains(t, desc, "endpoint")
		assert.Contains(t, desc, "event-types")
	})
}

func TestDiffResultLogChanges(t *testing.T) {
	t.Run("log add changes", func(t *testing.T) {
		diff := &DiffResult{
			Changes: []SinkChange{
				{
					Type:       ChangeAdd,
					Name:       "https://webhook.example.com",
					ChangeDesc: "Add sink https://webhook.example.com",
				},
			},
		}
		// Just verify it doesn't panic
		diff.LogChanges("users")
	})

	t.Run("log remove changes", func(t *testing.T) {
		diff := &DiffResult{
			Changes: []SinkChange{
				{
					Type:       ChangeRemove,
					Name:       "https://webhook.example.com",
					ChangeDesc: "Remove sink https://webhook.example.com",
				},
			},
		}
		diff.LogChanges("users")
	})

	t.Run("log update changes", func(t *testing.T) {
		diff := &DiffResult{
			Changes: []SinkChange{
				{
					Type:       ChangeUpdate,
					Name:       "https://webhook.example.com",
					ChangeDesc: "Update sink https://webhook.example.com (event-types)",
				},
			},
		}
		diff.LogChanges("users")
	})
}

// mockDispatcher is a test double for dispatcher interface
type mockDispatcher struct {
	sinks map[string][]string
}

func (m *mockDispatcher) Register(collection string, sink dispatch.Sink) {
	if sink != nil {
		m.sinks[collection] = append(m.sinks[collection], sink.Name())
	}
}

func (m *mockDispatcher) Remove(collection, name string) {
	sinks := m.sinks[collection]
	for i, d := range sinks {
		if d == name {
			m.sinks[collection] = append(sinks[:i], sinks[i+1:]...)
			return
		}
	}
}

func ptr(s string) *string {
	return &s
}
