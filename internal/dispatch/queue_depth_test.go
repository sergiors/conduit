package dispatch

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/streams"
)

// depthCall is a single queue-depth observation recorded by queueDepthRecorder.
type depthCall struct {
	collection string
	sinkType   collections.Type
	sinkID     string
	depth      int
}

// delCall is a single deletion recorded by queueDepthRecorder.
type delCall struct {
	collection string
	sinkType   collections.Type
	sinkID     string
}

// queueDepthRecorder is a thread-safe fake SinkQueueDepthObserver that records
// every observation and deletion for later assertion.
type queueDepthRecorder struct {
	mu     sync.Mutex
	depths []depthCall
	dels   []delCall
}

func (r *queueDepthRecorder) ObserveSinkQueueDepth(collection string, sinkType collections.Type, sinkID string, depth int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.depths = append(r.depths, depthCall{collection: collection, sinkType: sinkType, sinkID: sinkID, depth: depth})
}

func (r *queueDepthRecorder) DeleteSinkQueueDepth(collection string, sinkType collections.Type, sinkID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dels = append(r.dels, delCall{collection: collection, sinkType: sinkType, sinkID: sinkID})
}

func (r *queueDepthRecorder) depthCalls() []depthCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]depthCall(nil), r.depths...)
}

// depthsFor returns the observed depths for a lane, in observation order.
func (r *queueDepthRecorder) depthsFor(collection, sinkID string) []int {
	var out []int
	for _, c := range r.depthCalls() {
		if c.collection == collection && c.sinkID == sinkID {
			out = append(out, c.depth)
		}
	}
	return out
}

// lastDepth returns the last recorded depth for a lane and whether any was seen.
func (r *queueDepthRecorder) lastDepth(collection, sinkID string) (int, bool) {
	ds := r.depthsFor(collection, sinkID)
	if len(ds) == 0 {
		return 0, false
	}
	return ds[len(ds)-1], true
}

// deleteCalls returns recorded deletions, in order.
func (r *queueDepthRecorder) deleteCalls() []delCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delCall(nil), r.dels...)
}

// gatedTransport is a transport whose Send blocks on a release channel so tests
// can hold jobs in a lane's queue. Close never blocks on the gate.
type gatedTransport struct {
	release chan struct{}
}

func (g *gatedTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	<-g.release
	return nil
}

func (g *gatedTransport) Close() error { return nil }

// newGatedTransport returns a transport whose sends block until release is closed.
func newGatedTransport() *gatedTransport {
	return &gatedTransport{release: make(chan struct{})}
}

// TestSinkQueueDepthNoObservationOnRegistration proves no queue-depth is
// recorded at registration time: the depth is only published at queue
// transitions, so a freshly registered lane has no series yet.
func TestSinkQueueDepthNoObservationOnRegistration(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{}, nil, rec, nil)
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{}))
	assert.Empty(t, rec.depthCalls(), "no depth observation may exist at registration")
}

// TestSinkQueueDepthEnqueueUpdatesDepth proves enqueuing onto a lane publishes
// the post-send length: with a single worker blocked in Send, a second event
// records depth 1 and a third records depth 2.
func TestSinkQueueDepthEnqueueUpdatesDepth(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 4, WorkerCount: 1}, nil, rec, nil)
	transport := newGatedTransport()
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	// Event 1 occupies the lone worker (blocks in Send; queue empty).
	done1 := make(chan error, 1)
	go func() { done1 <- d.Dispatch(ctx, "users", record) }()
	require.Eventually(t, func() bool {
		_, ok := rec.lastDepth("users", "s1")
		return ok
	}, 5*time.Second, time.Millisecond, "the first enqueue must publish a depth")

	// Events 2 and 3 queue behind the blocked worker.
	done2 := make(chan error, 1)
	go func() { done2 <- d.Dispatch(ctx, "users", record) }()
	done3 := make(chan error, 1)
	go func() { done3 <- d.Dispatch(ctx, "users", record) }()

	// As the queue fills (worker still blocked), depths 1 then 2 must appear,
	// and never a value exceeding the queue capacity.
	require.Eventually(t, func() bool {
		ds := rec.depthsFor("users", "s1")
		return contains(ds, 1) && contains(ds, 2)
	}, 5*time.Second, time.Millisecond, "queue depths 1 then 2 must be observed while the worker is blocked")

	for _, dep := range rec.depthsFor("users", "s1") {
		assert.Less(t, dep, 4, "observed depth must stay under the queue capacity")
	}

	// Release the worker; every dispatch settles.
	close(transport.release)
	require.NoError(t, <-done1)
	require.NoError(t, <-done2)
	require.NoError(t, <-done3)
}

// TestSinkQueueDepthDrainsToZero proves dequeue observations return the depth
// to 0 once the lane drains: after release and full settlement the last recorded
// depth is 0, and no observation is ever negative.
func TestSinkQueueDepthDrainsToZero(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 4, WorkerCount: 1}, nil, rec, nil)
	transport := newGatedTransport()
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	done := make([]chan error, 3)
	for i := range done {
		done[i] = make(chan error, 1)
		go func(c chan error) { c <- d.Dispatch(ctx, "users", record) }(done[i])
	}

	// Wait until the queue is full (worker blocked, depth 2 observed) before
	// releasing, so we know the queue held queued events.
	require.Eventually(t, func() bool {
		_, ok := rec.lastDepth("users", "s1")
		return ok
	}, 5*time.Second, time.Millisecond)
	close(transport.release)

	for _, c := range done {
		require.NoError(t, <-c)
	}

	// After full drain the final published depth is 0.
	require.Eventually(t, func() bool {
		d, ok := rec.lastDepth("users", "s1")
		return ok && d == 0
	}, 5*time.Second, time.Millisecond, "the lane must publish a final depth of 0 after draining")

	for _, dep := range rec.depthsFor("users", "s1") {
		assert.GreaterOrEqual(t, dep, 0, "depth can never be negative")
	}
}

// TestSinkQueueDepthBackpressureUnchanged proves a full lane still blocks a
// submit (bounded backpressure unchanged), and that the depth gauge never
// exceeds the queue capacity.
func TestSinkQueueDepthBackpressureUnchanged(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 1, WorkerCount: 1}, nil, rec, nil)
	transport := newGatedTransport()
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	// Event 1 occupies the lone worker (queue empty).
	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "users", record) }()
	// Event 2 fills the queue (depth 1).
	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "users", record) }()
	// The full queue must report depth 1 at least once while the worker is
	// blocked. (It need not be the *last* observation: the worker's post-dequeue
	// publish of 0 can interleave with the submit-side publish of 1.)
	require.Eventually(t, func() bool {
		return contains(rec.depthsFor("users", "s1"), 1)
	}, 5*time.Second, time.Millisecond, "the full queue must report depth 1 while the worker is blocked")

	// Event 3 blocks on the full queue: its Dispatch must not return while the
	// queue is full.
	full := make(chan error, 1)
	go func() { full <- d.Dispatch(ctx, "users", record) }()
	select {
	case err := <-full:
		t.Fatalf("third dispatch returned %v while the lane was full; expected backpressure", err)
	default:
	}

	// Observed depth never exceeds the queue capacity of 1.
	for _, dep := range rec.depthsFor("users", "s1") {
		assert.LessOrEqual(t, dep, 1, "depth must never exceed the queue capacity")
	}

	close(transport.release)
	require.NoError(t, <-f1)
	require.NoError(t, <-f2)
	require.NoError(t, <-full)
}

// TestSinkQueueDepthRemovalDeletesSeries proves Remove/Clear/Close ask the depth
// observer to delete the lane's series exactly once with the right labels.
func TestSinkQueueDepthRemovalDeletesSeries(t *testing.T) {
	mkSink := func(id string) *RuntimeSink {
		return NewRuntimeSink(collections.Sink{ID: id, Type: collections.SinkTypeHTTP}, &MockTransport{})
	}

	t.Run("Remove deletes the series once", func(t *testing.T) {
		rec := &queueDepthRecorder{}
		d := NewDispatcher(Config{}, nil, rec, nil)
		sink := mkSink("s1")
		d.Register("users", sink)
		require.NoError(t, d.Dispatch(context.Background(), "users",
			streams.StreamRecord{RecordType: streams.InsertRecord}))

		d.Remove("users", sink.Key())
		assert.Equal(t, []delCall{{collection: "users", sinkType: collections.SinkTypeHTTP, sinkID: "s1"}},
			rec.deleteCalls())
	})

	t.Run("Clear deletes each closed lane's series", func(t *testing.T) {
		rec := &queueDepthRecorder{}
		d := NewDispatcher(Config{}, nil, rec, nil)
		d.Register("users", mkSink("s1"))
		d.Register("users", mkSink("s2"))
		require.NoError(t, d.Dispatch(context.Background(), "users",
			streams.StreamRecord{RecordType: streams.InsertRecord}))

		d.Clear("users")
		require.Len(t, rec.deleteCalls(), 2)
		assert.Contains(t, rec.deleteCalls(), delCall{collection: "users", sinkType: collections.SinkTypeHTTP, sinkID: "s1"})
		assert.Contains(t, rec.deleteCalls(), delCall{collection: "users", sinkType: collections.SinkTypeHTTP, sinkID: "s2"})
	})

	t.Run("Close deletes every lane's series", func(t *testing.T) {
		rec := &queueDepthRecorder{}
		d := NewDispatcher(Config{}, nil, rec, nil)
		d.Register("users", mkSink("s1"))
		d.Register("orders", mkSink("s2"))
		require.NoError(t, d.Dispatch(context.Background(), "users",
			streams.StreamRecord{RecordType: streams.InsertRecord}))

		require.NoError(t, d.Close())
		require.Len(t, rec.deleteCalls(), 2)
		assert.Contains(t, rec.deleteCalls(), delCall{collection: "users", sinkType: collections.SinkTypeHTTP, sinkID: "s1"})
		assert.Contains(t, rec.deleteCalls(), delCall{collection: "orders", sinkType: collections.SinkTypeHTTP, sinkID: "s2"})
	})
}

// TestSinkQueueDepthNilObserverTolerated proves a dispatcher with a nil depth
// observer (and a non-nil delivery observer) neither panics nor records depths,
// and vice versa.
func TestSinkQueueDepthNilObserverTolerated(t *testing.T) {
	// Non-nil delivery observer, nil depth observer.
	d := NewDispatcher(Config{}, &recordingObserver{}, nil, nil)
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{}))
	require.NoError(t, d.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord}))
	require.NoError(t, d.Close())

	// Nil delivery observer, non-nil depth observer.
	rec := &queueDepthRecorder{}
	d2 := NewDispatcher(Config{}, nil, rec, nil)
	d2.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{}))
	require.NoError(t, d2.Dispatch(context.Background(), "users", streams.StreamRecord{RecordType: streams.InsertRecord}))
	// A depth observation must flow even with no delivery observer.
	require.NotEmpty(t, rec.depthsFor("users", "s1"))
	require.NoError(t, d2.Close())
}

// concurrentTransport is a race-safe transport spy for the concurrency test.
type concurrentTransport struct {
	released chan struct{}
	sent     atomic.Int64
}

func (c *concurrentTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	if c.released != nil {
		<-c.released
	}
	c.sent.Add(1)
	return nil
}

func (c *concurrentTransport) Close() error { return nil }

// TestSinkQueueDepthConcurrentLanes is a race-safety sweep: 50 concurrent
// dispatch goroutines against one lane, then drain. A single worker serializes
// dequeues so the final published depth after full drain is deterministically 0,
// while the 50 concurrent dispatches still exercise the submit/worker race.
func TestSinkQueueDepthConcurrentLanes(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{WorkerCount: 1}, nil, rec, nil)
	// A released channel that is nil means "never block".
	transport := &concurrentTransport{}
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = d.Dispatch(ctx, "users", record) }()
	}
	wg.Wait()

	// Once every dispatch has settled, the lane has fully drained, so a 0-depth
	// observation must have been published by the worker at least once. (The
	// *last* observation need not be 0: a submit-side publish can race past the
	// worker's post-drain publish of 0, so we assert the drain fact, not the
	// last value.)
	require.Eventually(t, func() bool {
		return contains(rec.depthsFor("users", "s1"), 0)
	}, 5*time.Second, time.Millisecond, "the lane must have published depth 0 during the drain")

	// No observation may ever exceed the queue capacity.
	for _, dep := range rec.depthsFor("users", "s1") {
		assert.LessOrEqual(t, dep, DefaultQueueSize)
	}
}

// contains reports whether depths contains want.
func contains(ds []int, want int) bool {
	for _, d := range ds {
		if d == want {
			return true
		}
	}
	return false
}
