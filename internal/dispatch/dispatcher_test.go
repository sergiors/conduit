package dispatch

import (
	"context"
	"testing"

	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

// MockSink is a test double for Sink interface
type MockSink struct {
	name       string
	sent       bool
	closed     bool
	shouldFail bool
}

func (m *MockSink) Name() string {
	return m.name
}

func (m *MockSink) Send(ctx context.Context, record streams.StreamRecord) error {
	if m.shouldFail {
		return assert.AnError
	}
	m.sent = true
	return nil
}

func (m *MockSink) Close() error {
	m.closed = true
	return nil
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
		sink := &MockSink{name: "test"}

		d.Register("table1", sink)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.sinks["table1"], 1)
	})

	t.Run("register multiple sinks for same table", func(t *testing.T) {
		d := NewDispatcher()
		sink1 := &MockSink{name: "test1"}
		sink2 := &MockSink{name: "test2"}

		d.Register("table1", sink1)
		d.Register("table1", sink2)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.sinks["table1"], 2)
	})

	t.Run("register sinks for different tables", func(t *testing.T) {
		d := NewDispatcher()
		sink1 := &MockSink{name: "test1"}
		sink2 := &MockSink{name: "test2"}

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
		sink := &MockSink{name: "test"}
		d.Register("table1", sink)

		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "table1",
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"_id": "123"},
		}

		err := d.Dispatch(ctx, "table1", record)
		assert.NoError(t, err)
		assert.True(t, sink.sent)
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
		failDest := &MockSink{name: "fail", shouldFail: true}
		successDest := &MockSink{name: "success"}

		d.Register("table1", failDest)
		d.Register("table1", successDest)

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

func TestDispatcherClose(t *testing.T) {
	t.Run("close all sinks", func(t *testing.T) {
		d := NewDispatcher()
		sink1 := &MockSink{name: "sink1"}
		sink2 := &MockSink{name: "sink2"}

		d.Register("table1", sink1)
		d.Register("table2", sink2)

		err := d.Close()
		assert.NoError(t, err)
		assert.True(t, sink1.closed)
		assert.True(t, sink2.closed)
	})
}
