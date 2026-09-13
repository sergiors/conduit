package dispatch

import (
	"bytes"
	"context"
	"log"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/streams"
)

// safeBuffer is a minimal thread-safe writer+buffer for test capture: lane
// workers log via Write on their goroutine while the test reads via String, so
// a mutex guards both sides to keep -race clean.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// bufferLogger returns a *log.Logger writing to a *safeBuffer so tests can
// capture and inspect emitted delivery lines without racing the worker pool.
func bufferLogger(buf *safeBuffer) *log.Logger {
	return log.New(buf, "", 0)
}

// dispatchInsert sends one INSERT record to the given dispatcher's collection
// and waits for the delivery to complete.
func dispatchInsert(t *testing.T, d *Dispatcher, collection string) {
	t.Helper()
	err := d.Dispatch(context.Background(), collection, streams.StreamRecord{RecordType: streams.InsertRecord})
	require.NoError(t, err)
}

// TestLaneDeliveryLogging verifies the delivery-boundary logging emits one
// success/failure line per delivery attempt with the collection, sink ID, sink
// type and event type, and that it is skipped entirely when no logger is wired.
func TestLaneDeliveryLogging(t *testing.T) {
	t.Run("success logs succeeded line", func(t *testing.T) {
		var buf safeBuffer
		d := NewDispatcher(Config{}, nil, bufferLogger(&buf))

		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{})
		d.Register("users", sink)

		dispatchInsert(t, d, "users")
		assert.Contains(t, buf.String(),
			"sink delivery succeeded: collection=users sink_id=s1 sink_type=http event_type=INSERT")
	})

	t.Run("failure logs failed line with error", func(t *testing.T) {
		var buf safeBuffer
		d := NewDispatcher(Config{}, nil, bufferLogger(&buf))

		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{shouldFail: true})
		d.Register("users", sink)

		require.Error(t, d.Dispatch(context.Background(), "users",
			streams.StreamRecord{RecordType: streams.InsertRecord}))

		assert.Contains(t, buf.String(),
			"sink delivery failed: collection=users sink_id=s1 sink_type=http event_type=INSERT: "+assert.AnError.Error())
	})

	t.Run("empty sink id renders as dash", func(t *testing.T) {
		var buf safeBuffer
		d := NewDispatcher(Config{}, nil, bufferLogger(&buf))

		sink := NewRuntimeSink(collections.Sink{ID: "", Type: collections.SinkTypeHTTP}, &MockTransport{})
		d.Register("users", sink)

		dispatchInsert(t, d, "users")
		assert.Contains(t, buf.String(),
			"sink delivery succeeded: collection=users sink_id=- sink_type=http event_type=INSERT")
	})

	t.Run("no logger produces no delivery output", func(t *testing.T) {
		var buf safeBuffer
		// Observer present, logger nil: delivery metrics still flow but no logs.
		d := NewDispatcher(Config{}, &recordingObserver{}, nil)

		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{})
		d.Register("users", sink)

		dispatchInsert(t, d, "users")
		assert.Equal(t, "", buf.String(), "a logger-less dispatcher must emit no delivery logs")
	})

	t.Run("nil logger with failing delivery does not panic", func(t *testing.T) {
		d := NewDispatcher(Config{}, nil, nil) // nil observer and nil logger
		sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{shouldFail: true})
		d.Register("users", sink)

		err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord})
		require.Error(t, err, "delivery must still fail with a nil logger")
	})
}

// TestLaneDeliveryLoggingFilteredNoOp ensures a filtered no-op (Send returns nil
// without a transport call) still logs as success, matching the lane's existing
// success semantics.
func TestLaneDeliveryLoggingFilteredNoOp(t *testing.T) {
	var buf safeBuffer
	d := NewDispatcher(Config{}, nil, bufferLogger(&buf))

	// Sink accepts only INSERT; dispatch MODIFY so Send returns nil without a
	// transport reach. Outcome is success.
	sink := NewRuntimeSink(
		collections.Sink{ID: "s1", Type: collections.SinkTypeRedis, EventTypes: []string{"INSERT"}},
		&MockTransport{},
	)
	d.Register("users", sink)

	err := d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.ModifyRecord})
	require.NoError(t, err)

	assert.Contains(t, buf.String(),
		"sink delivery succeeded: collection=users sink_id=s1 sink_type=redis event_type=MODIFY",
		"a filtered no-op counts as success and is logged as succeeded")
}

// TestSinkIDOrDash verifies the dash substitution helper.
func TestSinkIDOrDash(t *testing.T) {
	assert.Equal(t, "-", sinkIDOrDash(""))
	assert.Equal(t, "s1", sinkIDOrDash("s1"))
	assert.Equal(t, "a b c", sinkIDOrDash("a b c"), "non-empty ids are returned verbatim")
}
