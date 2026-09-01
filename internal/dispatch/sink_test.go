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

func TestRuntimeSinkFilter(t *testing.T) {
	ctx := context.Background()

	t.Run("record matching filter is delivered", func(t *testing.T) {
		transport := &MockTransport{}
		sink := NewRuntimeSink(collections.Sink{
			Filter: collections.Filter{
				NewImage: collections.ImageFilter{
					"status": collections.FilterCondition{StartsWith: "active"},
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
			Filter: collections.Filter{
				NewImage: collections.ImageFilter{
					"status": collections.FilterCondition{StartsWith: "active"},
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

func TestRuntimeSinkFilterFlat(t *testing.T) {
	ctx := context.Background()
	// A single flat AND-only filter: newImage.tenant acme AND newImage.status
	// ACTIVE AND oldImage.deleted false. Delivered only when every declared
	// predicate matches.
	sink := NewRuntimeSink(collections.Sink{
		Filter: collections.Filter{
			NewImage: collections.ImageFilter{
				"tenant": collections.FilterCondition{Eq: "acme"},
				"status": collections.FilterCondition{Eq: "ACTIVE"},
			},
			OldImage: collections.ImageFilter{
				"deleted": collections.FilterCondition{Eq: false},
			},
		},
	}, &MockTransport{})

	t.Run("all declared predicates match is delivered", func(t *testing.T) {
		transport := &MockTransport{}
		sink.Transport = transport
		err := sink.Send(ctx, streams.StreamRecord{
			RecordType: streams.ModifyRecord,
			NewImage:   bson.M{"tenant": "acme", "status": "ACTIVE"},
			OldImage:   bson.M{"deleted": false},
		})
		assert.NoError(t, err)
		assert.True(t, transport.sent)
	})

	t.Run("newImage.status PENDING is skipped", func(t *testing.T) {
		transport := &MockTransport{}
		sink.Transport = transport
		err := sink.Send(ctx, streams.StreamRecord{
			RecordType: streams.ModifyRecord,
			NewImage:   bson.M{"tenant": "acme", "status": "PENDING"},
			OldImage:   bson.M{"deleted": false},
		})
		assert.NoError(t, err)
		assert.False(t, transport.sent)
	})

	t.Run("INSERT with declared oldImage block is skipped", func(t *testing.T) {
		transport := &MockTransport{}
		sink.Transport = transport
		err := sink.Send(ctx, streams.StreamRecord{
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"tenant": "acme", "status": "ACTIVE"},
		})
		assert.NoError(t, err)
		assert.False(t, transport.sent)
	})

	t.Run("newImage.tenant mismatch is skipped", func(t *testing.T) {
		transport := &MockTransport{}
		sink.Transport = transport
		err := sink.Send(ctx, streams.StreamRecord{
			RecordType: streams.ModifyRecord,
			NewImage:   bson.M{"tenant": "other", "status": "ACTIVE"},
			OldImage:   bson.M{"deleted": false},
		})
		assert.NoError(t, err)
		assert.False(t, transport.sent)
	})
}

func TestRuntimeSinkKey(t *testing.T) {
	sink := NewRuntimeSink(collections.Sink{
		ID:   "507f1f77bcf86cd799439011",
		Type: collections.SinkTypeHTTP,
		Spec: map[string]interface{}{"endpoint": "https://webhook.example.com"},
	}, &MockTransport{})

	assert.Equal(t, "507f1f77bcf86cd799439011", sink.Key())
}
