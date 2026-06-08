package destinations

import (
	"context"
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
)

func TestHTTPDestination(t *testing.T) {
	t.Run("creation with valid endpoint succeeds", func(t *testing.T) {
		dest, err := NewHTTPDestination("http://localhost:8080/events", "", []string{"INSERT", "MODIFY", "REMOVE"}, collections.FilterCriteria{})
		assert.NoError(t, err)
		assert.NotNil(t, dest)
		assert.Equal(t, "http://localhost:8080/events", dest.Name())
	})

	t.Run("creation with bearer token", func(t *testing.T) {
		dest, err := NewHTTPDestination("http://localhost:8080/events", "my-secret-token", []string{"INSERT"}, collections.FilterCriteria{})
		assert.NoError(t, err)
		assert.NotNil(t, dest)
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
	})

	t.Run("send filters by event type", func(t *testing.T) {
		dest, _ := NewHTTPDestination("http://localhost:8080/events", "", []string{"INSERT"}, collections.FilterCriteria{})

		ctx := context.Background()

		insertRecord := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.InsertRecord,
		}
		err := dest.Send(ctx, insertRecord)
		assert.Error(t, err)

		deleteRecord := streams.StreamRecord{
			TableName:  "orders",
			RecordType: streams.RemoveRecord,
		}
		err = dest.Send(ctx, deleteRecord)
		assert.NoError(t, err)
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
