package dispatch

import (
	"context"
	"testing"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

func TestRuntimeSinkEventTypeFiltering(t *testing.T) {
	ctx := context.Background()

	t.Run("allowed event type is delivered", func(t *testing.T) {
		transport := &MockTransport{}
		sink := NewRuntimeSink(collections.Sink{EventTypes: []string{"INSERT"}}, transport)

		err := sink.Send(ctx, streams.StreamRecord{RecordType: streams.InsertRecord})
		assert.NoError(t, err)
		assert.True(t, transport.sent)
	})

	t.Run("filtered event type is skipped", func(t *testing.T) {
		transport := &MockTransport{}
		sink := NewRuntimeSink(collections.Sink{EventTypes: []string{"INSERT"}}, transport)

		err := sink.Send(ctx, streams.StreamRecord{RecordType: streams.ModifyRecord})
		assert.NoError(t, err)
		assert.False(t, transport.sent)
	})

	t.Run("empty event types allows all", func(t *testing.T) {
		transport := &MockTransport{}
		sink := NewRuntimeSink(collections.Sink{}, transport)

		for _, rt := range []streams.RecordType{streams.InsertRecord, streams.ModifyRecord, streams.RemoveRecord} {
			transport.sent = false
			err := sink.Send(ctx, streams.StreamRecord{RecordType: rt})
			assert.NoError(t, err)
			assert.True(t, transport.sent, "expected %s to be allowed", rt)
		}
	})
}

func TestRuntimeSinkFilterCriteria(t *testing.T) {
	ctx := context.Background()

	t.Run("record matching filter is delivered", func(t *testing.T) {
		transport := &MockTransport{}
		sink := NewRuntimeSink(collections.Sink{
			FilterCriteria: collections.FilterCriteria{
				NewImage: collections.ImageFilter{
					"status": collections.FilterCondition{Prefix: strPtr("active")},
				},
			},
		}, transport)

		err := sink.Send(ctx, streams.StreamRecord{
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"status": "active_user"},
		})
		assert.NoError(t, err)
		assert.True(t, transport.sent)
	})

	t.Run("record not matching filter is skipped", func(t *testing.T) {
		transport := &MockTransport{}
		sink := NewRuntimeSink(collections.Sink{
			FilterCriteria: collections.FilterCriteria{
				NewImage: collections.ImageFilter{
					"status": collections.FilterCondition{Prefix: strPtr("active")},
				},
			},
		}, transport)

		err := sink.Send(ctx, streams.StreamRecord{
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"status": "inactive_user"},
		})
		assert.NoError(t, err)
		assert.False(t, transport.sent)
	})
}

func TestRuntimeSinkKey(t *testing.T) {
	sink := NewRuntimeSink(collections.Sink{
		ID:     "507f1f77bcf86cd799439011",
		Type:   collections.SinkTypeHTTP,
		Config: map[string]interface{}{"endpoint": "https://webhook.example.com"},
	}, &MockTransport{})

	assert.Equal(t, "507f1f77bcf86cd799439011", sink.Key())
}

func strPtr(s string) *string { return &s }
