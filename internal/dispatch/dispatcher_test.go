package dispatch

import (
	"context"
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

// MockTransport is a test double for the Transport interface.
type MockTransport struct {
	sent       bool
	closed     bool
	shouldFail bool
}

func (m *MockTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	if m.shouldFail {
		return assert.AnError
	}
	m.sent = true
	return nil
}

func (m *MockTransport) Close() error {
	m.closed = true
	return nil
}

func newTestSink(transport Transport) *RuntimeSink {
	return NewRuntimeSink(collections.Sink{}, transport)
}

func TestDispatcherCreation(t *testing.T) {
	t.Run("new dispatcher with empty sinks", func(t *testing.T) {
		d := NewDispatcher()
		assert.NotNil(t, d)
	})
}

func TestDispatcherRegistration(t *testing.T) {
	t.Run("register single sink", func(t *testing.T) {
		d := NewDispatcher()
		sink := newTestSink(&MockTransport{})

		d.Register("table1", sink)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.sinks["table1"], 1)
	})

	t.Run("register multiple sinks for same table", func(t *testing.T) {
		d := NewDispatcher()
		sink1 := newTestSink(&MockTransport{})
		sink2 := newTestSink(&MockTransport{})

		d.Register("table1", sink1)
		d.Register("table1", sink2)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.sinks["table1"], 2)
	})

	t.Run("register sinks for different tables", func(t *testing.T) {
		d := NewDispatcher()
		sink1 := newTestSink(&MockTransport{})
		sink2 := newTestSink(&MockTransport{})

		d.Register("table1", sink1)
		d.Register("table2", sink2)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.sinks["table1"], 1)
		assert.Len(t, d.sinks["table2"], 1)
	})
}

func TestDispatcherDispatch(t *testing.T) {
	t.Run("dispatch to registered sinks", func(t *testing.T) {
		d := NewDispatcher()
		transport := &MockTransport{}
		d.Register("table1", newTestSink(transport))

		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "table1",
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"_id": "123"},
		}

		err := d.Dispatch(ctx, "table1", record)
		assert.NoError(t, err)
		assert.True(t, transport.sent)
	})

	t.Run("dispatch to unregistered table does nothing", func(t *testing.T) {
		d := NewDispatcher()
		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "unknown",
			RecordType: streams.InsertRecord,
		}

		err := d.Dispatch(ctx, "unknown", record)
		assert.NoError(t, err)
	})

	t.Run("dispatch continues on failure", func(t *testing.T) {
		d := NewDispatcher()
		failDest := &MockTransport{shouldFail: true}
		successDest := &MockTransport{}

		d.Register("table1", newTestSink(failDest))
		d.Register("table1", newTestSink(successDest))

		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "table1",
			RecordType: streams.ModifyRecord,
		}

		err := d.Dispatch(ctx, "table1", record)
		assert.Error(t, err)
		assert.True(t, successDest.sent)
	})
}

func TestDispatcherRemove(t *testing.T) {
	t.Run("remove sink by key", func(t *testing.T) {
		d := NewDispatcher()
		sink := newTestSink(&MockTransport{})
		d.Register("table1", sink)

		d.Remove("table1", sink.Key())

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Empty(t, d.sinks["table1"])
	})

	t.Run("remove missing key is a no-op", func(t *testing.T) {
		d := NewDispatcher()
		sink := newTestSink(&MockTransport{})
		d.Register("table1", sink)

		d.Remove("table1", "missing-key")

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.sinks["table1"], 1)
	})
}

func TestDispatcherClear(t *testing.T) {
	t.Run("clear removes all sinks for a collection", func(t *testing.T) {
		d := NewDispatcher()
		transport := &MockTransport{}
		d.Register("table1", newTestSink(transport))

		d.Clear("table1")

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Empty(t, d.sinks["table1"])
		assert.True(t, transport.closed)
	})
}

// countingTransport is a transport spy that counts sends and closes so tests
// can assert that an update preserves the live transport.
type countingTransport struct {
	sent   int
	closed int
}

func (c *countingTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	c.sent++
	return nil
}

func (c *countingTransport) Close() error {
	c.closed++
	return nil
}

func TestDispatcherUpdateKeepsTransport(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()
	transport := &countingTransport{}

	// Register a sink that only accepts INSERT events.
	original := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "https://example.com"},
		EventTypes: []string{"INSERT"},
	}
	d.Register("table1", NewRuntimeSink(original, transport))

	// Update the sink's mutable config: filter + event types.
	updated := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "https://example.com"},
		EventTypes: []string{"MODIFY"},
		Filter: collections.Filter{
			NewImage: collections.ImageFilter{
				"status": collections.FilterCondition{Eq: "active"},
			},
		},
	}
	assert.True(t, d.Update("table1", updated), "update of a registered sink should succeed")

	// The transport must be preserved, not closed.
	assert.Zero(t, transport.closed, "update must not close the live transport")

	// The same *RuntimeSink pointer must be retained (identity stable).
	d.mu.RLock()
	rs := d.sinks["table1"][0]
	d.mu.RUnlock()
	assert.Equal(t, "s1", rs.Key())

	// Events matching the NEW filter and event type are delivered.
	err := d.Dispatch(ctx, "table1", streams.StreamRecord{
		RecordType: streams.ModifyRecord,
		NewImage:   bson.M{"status": "active"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "event matching new filter/event type should be delivered")

	// An event type excluded by the new eventTypes is not delivered.
	err = d.Dispatch(ctx, "table1", streams.StreamRecord{
		RecordType: streams.InsertRecord,
		NewImage:   bson.M{"status": "active"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "INSERT is excluded by new eventTypes")

	// An event matching the new event type but not the new filter is not delivered.
	err = d.Dispatch(ctx, "table1", streams.StreamRecord{
		RecordType: streams.ModifyRecord,
		NewImage:   bson.M{"status": "paused"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "event not matching new filter should be skipped")
}

func TestDispatcherUpdateMissingReturnsFalse(t *testing.T) {
	d := NewDispatcher()
	d.Register("table1", newTestSink(&MockTransport{}))

	// Updating an unregistered key returns false and does not panic.
	assert.False(t, d.Update("table1", collections.Sink{ID: "missing"}))
	assert.False(t, d.Update("unknown", collections.Sink{ID: "s1"}))
}

func TestDispatcherUpdateEventTypesNormalization(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()
	transport := &countingTransport{}

	d.Register("table1", NewRuntimeSink(collections.Sink{ID: "s1"}, transport))

	// Update with lowercase event types; UpdateConfig must normalize to uppercase.
	assert.True(t, d.Update("table1", collections.Sink{ID: "s1", EventTypes: []string{"insert"}}))

	err := d.Dispatch(ctx, "table1", streams.StreamRecord{RecordType: streams.InsertRecord})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "lowercase event type should be normalized and match INSERT")
}

func TestDispatcherUpdateConcurrentSend(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()
	transport := &countingTransport{}

	d.Register("table1", NewRuntimeSink(collections.Sink{ID: "s1", EventTypes: []string{"INSERT"}}, transport))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			d.Update("table1", collections.Sink{ID: "s1", EventTypes: []string{"MODIFY"}})
			d.Update("table1", collections.Sink{ID: "s1", EventTypes: []string{"INSERT"}})
		}
	}()

	for i := 0; i < 1000; i++ {
		_ = d.Dispatch(ctx, "table1", streams.StreamRecord{RecordType: streams.InsertRecord})
	}
	<-done
}

func TestDispatcherClose(t *testing.T) {
	t.Run("close all sinks", func(t *testing.T) {
		d := NewDispatcher()
		transport1 := &MockTransport{}
		transport2 := &MockTransport{}

		d.Register("table1", newTestSink(transport1))
		d.Register("table2", newTestSink(transport2))

		err := d.Close()
		assert.NoError(t, err)
		assert.True(t, transport1.closed)
		assert.True(t, transport2.closed)
	})
}
