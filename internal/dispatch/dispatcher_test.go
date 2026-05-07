package dispatch

import (
	"context"
	"testing"

	"github.com/sergiors/conduit/internal/streams"
	"github.com/sergiors/conduit/internal/collections"
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

func TestHTTPDestination(t *testing.T) {
	t.Run("creation with valid endpoint succeeds", func(t *testing.T) {
		dest, err := NewHTTPDestination("http://localhost:8080/events", "", []string{"INSERT", "MODIFY", "DELETE"}, collections.FilterCriteria{})
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.Equal(t, "http://localhost:8080/events", dest.endpoint)
		assert.Equal(t, "", dest.bearerToken)
	})

	t.Run("creation with bearer token", func(t *testing.T) {
		dest, err := NewHTTPDestination("http://localhost:8080/events", "my-secret-token", []string{"INSERT"}, collections.FilterCriteria{})
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.Equal(t, "my-secret-token", dest.bearerToken)
	})

	t.Run("creation with empty endpoint fails", func(t *testing.T) {
		dest, err := NewHTTPDestination("", "", []string{"INSERT"}, collections.FilterCriteria{})
		assert.Error(t, err)
		assert.Nil(t, dest)
	})

	t.Run("creation with empty event types defaults to all", func(t *testing.T) {
		dest, err := NewHTTPDestination("http://localhost:8080/events", "", []string{}, collections.FilterCriteria{})
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.True(t, dest.eventTypes["INSERT"])
		assert.True(t, dest.eventTypes["MODIFY"])
		assert.True(t, dest.eventTypes["REMOVE"])
	})

	t.Run("send filters by event type", func(t *testing.T) {
		// Create destination that only accepts INSERT events
		dest, _ := NewHTTPDestination("http://localhost:8080/events", "", []string{"INSERT"}, collections.FilterCriteria{})

		ctx := context.Background()

		// INSERT should be sent (will fail to connect, but filter passes)
		insertRecord := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.InsertRecord,
		}
		err := dest.Send(ctx, insertRecord)
		assert.Error(t, err) // Connection error expected

		// DELETE should be filtered out (no error, just skipped)
		deleteRecord := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.RemoveRecord,
		}
		err = dest.Send(ctx, deleteRecord)
		assert.NoError(t, err) // Filtered out silently
	})

	t.Run("close succeeds", func(t *testing.T) {
		dest, _ := NewHTTPDestination("http://localhost:8080/events", "", []string{"INSERT"}, collections.FilterCriteria{})
		err := dest.Close()
		assert.NoError(t, err)
	})
}

func TestEventBridgeDestination(t *testing.T) {
	t.Run("creation succeeds", func(t *testing.T) {
		dest, err := NewEventBridgeDestination("us-east-1", "my-event-bus", "conduit-mongodb", "")
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.Equal(t, "eventbridge:my-event-bus@us-east-1", dest.Name())
	})

	t.Run("creation fails without region", func(t *testing.T) {
		dest, err := NewEventBridgeDestination("", "my-event-bus", "", "")
		assert.Error(t, err)
		assert.Nil(t, dest)
	})

	t.Run("creation fails without event bus", func(t *testing.T) {
		dest, err := NewEventBridgeDestination("us-east-1", "", "", "")
		assert.Error(t, err)
		assert.Nil(t, dest)
	})

	t.Run("send logs but does not fail", func(t *testing.T) {
		dest, _ := NewEventBridgeDestination("us-east-1", "my-event-bus", "conduit-mongodb", "")
		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.ModifyRecord,
		}

		err := dest.Send(ctx, record)
		assert.NoError(t, err)
	})

	t.Run("close succeeds", func(t *testing.T) {
		dest, _ := NewEventBridgeDestination("us-east-1", "my-event-bus", "conduit-mongodb", "")
		err := dest.Close()
		assert.NoError(t, err)
	})
}

func TestMeilisearchDestination(t *testing.T) {
	t.Run("creation succeeds", func(t *testing.T) {
		dest, err := NewMeilisearchDestination("http://localhost:7700", "master-key", "orders")
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.Equal(t, "meilisearch:http://localhost:7700/orders", dest.Name())
	})

	t.Run("creation fails without host", func(t *testing.T) {
		dest, err := NewMeilisearchDestination("", "master-key", "orders")
		assert.Error(t, err)
		assert.Nil(t, dest)
	})

	t.Run("creation fails without index", func(t *testing.T) {
		dest, err := NewMeilisearchDestination("http://localhost:7700", "master-key", "")
		assert.Error(t, err)
		assert.Nil(t, dest)
	})

	t.Run("send logs but does not fail", func(t *testing.T) {
		dest, _ := NewMeilisearchDestination("http://localhost:7700", "master-key", "orders")
		ctx := context.Background()
		record := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.InsertRecord,
		}

		err := dest.Send(ctx, record)
		assert.NoError(t, err)
	})

	t.Run("close succeeds", func(t *testing.T) {
		dest, _ := NewMeilisearchDestination("http://localhost:7700", "master-key", "orders")
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
