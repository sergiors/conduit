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
