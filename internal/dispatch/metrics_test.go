package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/streams"
)

// recordingObserver is a thread-safe fake SinkDeliveryObserver.
type recordingObserver struct {
	mu           sync.Mutex
	obs          []obsCall
	deliveryErrs []error
}

type obsCall struct {
	collection string
	sinkType   collections.Type
}

func (r *recordingObserver) ObserveSinkDelivery(collection string, sinkType collections.Type, duration time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.obs = append(r.obs, obsCall{collection: collection, sinkType: sinkType})
	r.deliveryErrs = append(r.deliveryErrs, err)
}

func (r *recordingObserver) calls() []obsCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]obsCall(nil), r.obs...)
}

func (r *recordingObserver) errs() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.deliveryErrs...)
}

// TestDispatcherObserverDeliveries verifies the observer is notified for real
// delivery attempts with the correct collection and sink type, and that outcome
// reflects the Send result.
func TestDispatcherObserverDeliveries(t *testing.T) {
	t.Run("success delivers one success observation", func(t *testing.T) {
		obs := &recordingObserver{}
		d := NewDispatcherWithObserver(Config{}, obs)

		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{})
		d.Register("users", sink)

		ctx := context.Background()
		err := d.Dispatch(ctx, "users", streams.StreamRecord{RecordType: streams.InsertRecord})
		require.NoError(t, err)

		calls := obs.calls()
		require.Len(t, calls, 1)
		assert.Equal(t, "users", calls[0].collection)
		assert.Equal(t, collections.SinkTypeHTTP, calls[0].sinkType)
		assert.NoError(t, obs.errs()[0])
	})

	t.Run("failure delivers one failure observation", func(t *testing.T) {
		obs := &recordingObserver{}
		d := NewDispatcherWithObserver(Config{}, obs)

		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{shouldFail: true})
		d.Register("users", sink)

		err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord})
		require.Error(t, err)

		calls := obs.calls()
		require.Len(t, calls, 1)
		assert.Equal(t, "users", calls[0].collection)
		assert.Error(t, obs.errs()[0])
	})

	t.Run("no matching sinks produces no observation", func(t *testing.T) {
		obs := &recordingObserver{}
		d := NewDispatcherWithObserver(Config{}, obs)

		// Register a sink for "users" but dispatch to "orders": no lanes match.
		d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1"}, &MockTransport{}))

		err := d.Dispatch(context.Background(), "orders", streams.StreamRecord{RecordType: streams.InsertRecord})
		require.NoError(t, err)
		assert.Empty(t, obs.calls(), "no lanes matched, so no delivery observed")
	})

	t.Run("filtered no-op counts as success for the sink lane", func(t *testing.T) {
		obs := &recordingObserver{}
		d := NewDispatcherWithObserver(Config{}, obs)

		// A sink that only accepts INSERT; dispatch MODIFY so Send returns nil
		// without a transport call. Outcome reflects the per-sink lane result.
		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeRedis, EventTypes: []string{"INSERT"}}, &MockTransport{})
		d.Register("users", sink)

		err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.ModifyRecord})
		require.NoError(t, err)

		calls := obs.calls()
		require.Len(t, calls, 1)
		assert.Equal(t, "users", calls[0].collection)
		assert.Equal(t, collections.SinkTypeRedis, calls[0].sinkType)
		assert.NoError(t, obs.errs()[0], "a filtered no-op returns nil and counts as success for the lane")
	})
}

// TestNewDispatcherDefaultNoObserver proves the default constructor works with a
// nil observer (no panics, no observations).
func TestNewDispatcherDefaultNoObserver(t *testing.T) {
	d := NewDispatcher()
	require.NotNil(t, d)
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1"}, &MockTransport{}))
	err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord})
	require.NoError(t, err)
}

// TestNewDispatcherWithObserverNilObserver proves a nil observer is tolerated.
func TestNewDispatcherWithObserverNilObserver(t *testing.T) {
	d := NewDispatcherWithObserver(Config{}, nil)
	require.NotNil(t, d)
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1"}, &MockTransport{}))
	err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord})
	require.NoError(t, err)
}

// TestDispatcherObserverErrorIsolation verifies a per-sink observer that
// records multiple concurrent lanes reports each independently.
func TestDispatcherObserverMultipleLanes(t *testing.T) {
	obs := &recordingObserver{}
	d := NewDispatcherWithObserver(Config{}, obs)

	good := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{})
	bad := NewRuntimeSink(collections.Sink{ID: "s2", Type: collections.SinkTypeRedis}, &MockTransport{shouldFail: true})
	d.Register("users", good)
	d.Register("users", bad)

	err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord})
	require.Error(t, err, "a failing lane fails dispatch")

	calls := obs.calls()
	require.Len(t, calls, 2, "one observation per matching sink lane")

	// The two lanes deliver concurrently, so observation order is not
	// deterministic; assert the multiset of outcomes instead.
	var successCount, failureCount int
	for _, err := range obs.errs() {
		if err != nil {
			failureCount++
		} else {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "the good lane must observe success")
	assert.Equal(t, 1, failureCount, "the bad lane must observe failure")

	// Both lanes must carry the correct collection + sink-type labels.
	sinkTypes := map[collections.Type]int{}
	for _, c := range calls {
		assert.Equal(t, "users", c.collection)
		sinkTypes[c.sinkType]++
	}
	assert.Equal(t, 1, sinkTypes[collections.SinkTypeHTTP])
	assert.Equal(t, 1, sinkTypes[collections.SinkTypeRedis])
}
