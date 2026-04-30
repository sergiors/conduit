package dispatch

import (
	"context"
	"testing"

	"github.com/relay-mongodb/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

func TestDispatcherCreation(t *testing.T) {
	t.Run("new dispatcher with empty destinations", func(t *testing.T) {
		d := NewDispatcher()
		assert.NotNil(t, d)
	})
}

func TestDispatcherRegistration(t *testing.T) {
	t.Run("register single destination", func(t *testing.T) {
		d := NewDispatcher()
		dest := &MockDestination{name: "test"}

		d.Register("table1", dest)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.destinations["table1"], 1)
	})

	t.Run("register multiple destinations for same table", func(t *testing.T) {
		d := NewDispatcher()
		dest1 := &MockDestination{name: "test1"}
		dest2 := &MockDestination{name: "test2"}

		d.Register("table1", dest1)
		d.Register("table1", dest2)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.destinations["table1"], 2)
	})

	t.Run("register destinations for different tables", func(t *testing.T) {
		d := NewDispatcher()
		dest1 := &MockDestination{name: "test1"}
		dest2 := &MockDestination{name: "test2"}

		d.Register("table1", dest1)
		d.Register("table2", dest2)

		d.mu.RLock()
		defer d.mu.RUnlock()
		assert.Len(t, d.destinations["table1"], 1)
		assert.Len(t, d.destinations["table2"], 1)
	})
}

func TestDispatcherDispatch(t *testing.T) {
	t.Run("dispatch to registered destinations", func(t *testing.T) {
		d := NewDispatcher()
		dest := &MockDestination{name: "test"}
		d.Register("table1", dest)

		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "table1",
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"_id": "123"},
		}

		err := d.Dispatch(ctx, "table1", record)
		assert.NoError(t, err)
		assert.True(t, dest.sent)
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
		failDest := &MockDestination{name: "fail", shouldFail: true}
		successDest := &MockDestination{name: "success"}

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
	t.Run("close all destinations", func(t *testing.T) {
		d := NewDispatcher()
		dest1 := &MockDestination{name: "dest1"}
		dest2 := &MockDestination{name: "dest2"}

		d.Register("table1", dest1)
		d.Register("table2", dest2)

		err := d.Close()
		assert.NoError(t, err)
		assert.True(t, dest1.closed)
		assert.True(t, dest2.closed)
	})
}

func TestRedisDestination(t *testing.T) {
	t.Run("creation with invalid URI fails", func(t *testing.T) {
		// This will fail because Redis is not running, which is expected
		dest, err := NewRedisDestination("redis://invalid:6379")
		assert.Error(t, err)
		assert.Nil(t, dest)
	})
}

func TestEventBridgeDestination(t *testing.T) {
	t.Run("creation succeeds", func(t *testing.T) {
		dest, err := NewEventBridgeDestination("my-event-bus")
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.Equal(t, "eventbridge:my-event-bus", dest.Name())
	})

	t.Run("send logs but does not fail", func(t *testing.T) {
		dest, _ := NewEventBridgeDestination("my-event-bus")
		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.ModifyRecord,
		}

		err := dest.Send(ctx, record)
		assert.NoError(t, err)
	})

	t.Run("close succeeds", func(t *testing.T) {
		dest, _ := NewEventBridgeDestination("my-event-bus")
		err := dest.Close()
		assert.NoError(t, err)
	})
}

// MockDestination is a test double for Destination interface
type MockDestination struct {
	name       string
	sent       bool
	closed     bool
	shouldFail bool
}

func (m *MockDestination) Name() string {
	return m.name
}

func (m *MockDestination) Send(ctx context.Context, record streams.StreamRecord) error {
	if m.shouldFail {
		return assert.AnError
	}
	m.sent = true
	return nil
}

func (m *MockDestination) Close() error {
	m.closed = true
	return nil
}
