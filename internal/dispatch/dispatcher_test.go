package dispatch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

// MockTransport is a test double for Transport.
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

// countingTransport is a transport spy that counts sends and closes so tests
// can assert that an update preserves the live transport.
type countingTransport struct {
	sent   int
	closed int
}

func (c *countingTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	c.sent++
	return nil
}

func (c *countingTransport) Close() error {
	c.closed++
	return nil
}

func TestDispatcherUpdateKeepsTransport(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()
	transport := &countingTransport{}

	// Register a sink that only accepts INSERT events.
	original := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "https://example.com"},
		EventTypes: []string{"INSERT"},
	}
	d.Register("table1", NewRuntimeSink(original, transport))

	// Update the sink's mutable config: filter + event types.
	updated := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeHTTP,
		Spec:       map[string]interface{}{"endpoint": "https://example.com"},
		EventTypes: []string{"MODIFY"},
		Filter: collections.Filter{
			NewImage: collections.ImageFilter{
				"status": collections.FilterCondition{Eq: "active"},
			},
		},
	}
	assert.True(t, d.Update("table1", updated), "update of a registered sink should succeed")

	// The transport must be preserved, not closed.
	assert.Zero(t, transport.closed, "update must not close the live transport")

	// The same *RuntimeSink pointer must be retained (identity stable).
	d.mu.RLock()
	rs := d.sinks["table1"][0].sink
	d.mu.RUnlock()
	assert.Equal(t, "s1", rs.Key())

	// Events matching the NEW filter and event type are delivered.
	err := d.Dispatch(ctx, "table1", streams.StreamRecord{
		RecordType: streams.ModifyRecord,
		NewImage:   bson.M{"status": "active"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "event matching new filter/event type should be delivered")

	// An event type excluded by the new eventTypes is not delivered.
	err = d.Dispatch(ctx, "table1", streams.StreamRecord{
		RecordType: streams.InsertRecord,
		NewImage:   bson.M{"status": "active"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "INSERT is excluded by new eventTypes")

	// An event matching the new event type but not the new filter is not delivered.
	err = d.Dispatch(ctx, "table1", streams.StreamRecord{
		RecordType: streams.ModifyRecord,
		NewImage:   bson.M{"status": "paused"},
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "event not matching new filter should be skipped")
}

func TestDispatcherUpdateMissingReturnsFalse(t *testing.T) {
	d := NewDispatcher()
	d.Register("table1", newTestSink(&MockTransport{}))

	// Updating an unregistered key returns false and does not panic.
	assert.False(t, d.Update("table1", collections.Sink{ID: "missing"}))
	assert.False(t, d.Update("unknown", collections.Sink{ID: "s1"}))
}

func TestDispatcherUpdateEventTypesNormalization(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()
	transport := &countingTransport{}

	d.Register("table1", NewRuntimeSink(collections.Sink{ID: "s1"}, transport))

	// Update with lowercase event types; UpdateConfig must normalize to uppercase.
	assert.True(t, d.Update("table1", collections.Sink{ID: "s1", EventTypes: []string{"insert"}}))

	err := d.Dispatch(ctx, "table1", streams.StreamRecord{RecordType: streams.InsertRecord})
	assert.NoError(t, err)
	assert.Equal(t, 1, transport.sent, "lowercase event type should be normalized and match INSERT")
}

func TestDispatcherUpdateConcurrentSend(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()
	transport := &countingTransport{}

	d.Register("table1", NewRuntimeSink(collections.Sink{ID: "s1", EventTypes: []string{"INSERT"}}, transport))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			d.Update("table1", collections.Sink{ID: "s1", EventTypes: []string{"MODIFY"}})
			d.Update("table1", collections.Sink{ID: "s1", EventTypes: []string{"INSERT"}})
		}
	}()

	for i := 0; i < 1000; i++ {
		_ = d.Dispatch(ctx, "table1", streams.StreamRecord{RecordType: streams.InsertRecord})
	}
	<-done
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

// blockingTransport is a transport whose Send blocks on a gate channel.
// A nil gate signals the gate is already open.
type blockingTransport struct {
	entered chan struct{} // closed once Send has been entered (first call)
	gate    chan struct{} // Send blocks here until it is closed
}

func newBlockingTransport() *blockingTransport {
	return &blockingTransport{
		entered: make(chan struct{}),
		gate:    make(chan struct{}),
	}
}

func (b *blockingTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	select {
	case <-b.entered:
	default:
		close(b.entered)
	}
	<-b.gate
	return nil
}

func (b *blockingTransport) Close() error {
	// Never block on the gate; a transport Close must not deadlock.
	return nil
}

// atomicTransport is a race-safe transport spy: a worker goroutine may still be
// writing `sent` when the test asserts it, so completion is read atomically.
type atomicTransport struct {
	sentFlag atomic.Bool
}

func (a *atomicTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	a.sentFlag.Store(true)
	return nil
}

func (a *atomicTransport) Close() error { return nil }

func (a *atomicTransport) sent() bool { return a.sentFlag.Load() }

// TestDispatcherDispatchConcurrent proves Dispatch delivers to sinks in
// parallel rather than sequentially. A slow (blocking) sink is registered
// first; under sequential fan-out it would block Dispatch before the fast sink
// was touched, so the fast sink completing while the slow sink is still blocked
// is a deterministic proof of concurrency (no sleeps).
func TestDispatcherDispatchConcurrent(t *testing.T) {
	d := NewDispatcher()
	fast := &atomicTransport{}
	slow := newBlockingTransport()

	// slow first: sequential dispatch would get stuck on it and never reach fast.
	d.Register("table1", newTestSink(slow))
	d.Register("table1", newTestSink(fast))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	done := make(chan error, 1)
	go func() { done <- d.Dispatch(ctx, "table1", record) }()

	select {
	case <-slow.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow sink never entered Send")
	}

	// Fast sink must complete while slow is still blocked; sequential dispatch
	// would hang here until we release the gate.
	release := make(chan struct{})
	go func() {
		select {
		case <-slow.gate:
			close(release)
		case <-time.After(5 * time.Second):
		}
	}()
	select {
	case <-release:
		t.Fatal("fast sink was not reached while slow sink was blocked; dispatch appears sequential")
	case <-time.After(500 * time.Millisecond):
	}
	assert.True(t, fast.sent(), "fast sink should have delivered while slow was blocked")

	// Release the slow sink and confirm Dispatch completes with no error.
	close(slow.gate)
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch did not return after releasing the slow sink")
	}
}

// TestDispatcherBackpressure proves a full sink lane applies bounded
// backpressure: a Dispatch submission blocks (rather than dropping) until a
// worker frees capacity. A single worker and queue size 1 means a second
// concurrent event cannot be accepted until the first is delivered.
func TestDispatcherBackpressure(t *testing.T) {
	d := NewDispatcherWithConfig(Config{QueueSize: 1, WorkerCount: 1})
	slow := newBlockingTransport()
	d.Register("table1", newTestSink(slow))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	// First dispatch occupies the single worker (blocks in Send).
	firstDone := make(chan error, 1)
	go func() { firstDone <- d.Dispatch(ctx, "table1", record) }()
	<-slow.entered

	secondStarted := make(chan error, 1)
	go func() { secondStarted <- d.Dispatch(ctx, "table1", record) }()

	select {
	case <-slow.gate:
		t.Fatal("gate closed before the second dispatch could be tested")
	default:
	}

	// The second dispatch must still be blocked on the full lane, not returned
	// (which would mean it was dropped).
	select {
	case err := <-secondStarted:
		t.Fatalf("second dispatch returned %v while the lane was full; expected backpressure, not drop", err)
	case <-time.After(300 * time.Millisecond):
	}

	// Release the first worker; both dispatches should now complete.
	close(slow.gate)
	assert.NoError(t, <-firstDone)
	assert.NoError(t, <-secondStarted)
}

// TestNewDispatcherWithConfigSanitization verifies zero/negative config values
// fall back to the defaults instead of producing a degenerate lane.
func TestNewDispatcherWithConfigSanitization(t *testing.T) {
	t.Run("zero config uses defaults", func(t *testing.T) {
		d := NewDispatcherWithConfig(Config{})
		assert.Equal(t, DefaultQueueSize, d.cfg.QueueSize)
		assert.Equal(t, DefaultWorkerCount, d.cfg.WorkerCount)
	})

	t.Run("negative config uses defaults", func(t *testing.T) {
		d := NewDispatcherWithConfig(Config{QueueSize: -1, WorkerCount: -5})
		assert.Equal(t, DefaultQueueSize, d.cfg.QueueSize)
		assert.Equal(t, DefaultWorkerCount, d.cfg.WorkerCount)
	})

	t.Run("partial config only overrides provided fields", func(t *testing.T) {
		d := NewDispatcherWithConfig(Config{QueueSize: 8})
		assert.Equal(t, 8, d.cfg.QueueSize)
		assert.Equal(t, DefaultWorkerCount, d.cfg.WorkerCount)

		d2 := NewDispatcherWithConfig(Config{WorkerCount: 2})
		assert.Equal(t, DefaultQueueSize, d2.cfg.QueueSize)
		assert.Equal(t, 2, d2.cfg.WorkerCount)
	})

	t.Run("explicit values are preserved", func(t *testing.T) {
		d := NewDispatcherWithConfig(Config{QueueSize: 16, WorkerCount: 8})
		assert.Equal(t, 16, d.cfg.QueueSize)
		assert.Equal(t, 8, d.cfg.WorkerCount)
	})
}

// TestDispatcherCloseDuringDispatch races Close against in-flight Dispatch
// calls (with a fast transport) to prove Close neither panics nor drops accepted
// jobs. Close may legitimately wait for an in-flight delivery to settle (the
// settlement contract), so this uses a fast transport; the watcher is stopped
// before Close in production, so a blocked slow transport during Close is not a
// case the dispatcher must abort.
func TestDispatcherCloseDuringDispatch(t *testing.T) {
	d := NewDispatcher()
	transport := &countingTransport{}
	d.Register("table1", NewRuntimeSink(collections.Sink{ID: "s1"}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = d.Dispatch(ctx, "table1", record)
		}
	}()

	// Close while dispatches are in flight.
	if err := d.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	<-done

	// Dispatch after Close must be safe and report no sinks (empty registry),
	// not panic.
	assert.NoError(t, d.Dispatch(ctx, "table1", record))

	// Close is idempotent.
	assert.NoError(t, d.Close())
}

// TestDispatcherDispatchContextCancel proves a cancelled context surfaces as a
// non-nil error from Dispatch rather than hanging or dropping. With workerCount
// 1 and queueSize 1, two prior dispatches occupy the lone worker and its queue;
// a third dispatch then blocks on submission and sees the context cancel.
func TestDispatcherDispatchContextCancel(t *testing.T) {
	d := NewDispatcherWithConfig(Config{QueueSize: 1, WorkerCount: 1})
	slow := newBlockingTransport()
	d.Register("table1", newTestSink(slow))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	// dispatch1 occupies the lone worker (blocks in Send; queue now empty).
	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "table1", record) }()
	<-slow.entered

	// dispatch2 fills the queue (worker is blocked, so this job stays queued).
	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "table1", record) }()
	time.Sleep(100 * time.Millisecond)

	// dispatch3 blocks on the full queue; cancel its context to force a submit
	// failure. Dispatch must return promptly with the context error, not hang.
	cctx, cancel := context.WithCancel(context.Background())
	f3 := make(chan error, 1)
	go func() { f3 <- d.Dispatch(cctx, "table1", record) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	// The cancelled submit must contribute exactly one result to the wait loop.
	select {
	case err := <-f3:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch hung on cancelled context with a full lane")
	}

	// Release the worker so the other two lanes settle and the test exits cleanly.
	close(slow.gate)
	<-f1
	<-f2
}

// TestDispatcherCloseWhileBackpressured proves close cannot deadlock against a
// submit blocked on a full lane queue. With workerCount 1 and queueSize 1, a
// first dispatch occupies the lone worker (blocked in Send) and a second fills
// the queue; a third dispatch then blocks on submission. Closing the lane must
// unblock that submit (with errLaneClosed) promptly. Close itself still waits
// for the in-flight worker to finish (the settlement contract), so the gate is
// released after the backpressured submit is proven unblocked.
func TestDispatcherCloseWhileBackpressured(t *testing.T) {
	d := NewDispatcherWithConfig(Config{QueueSize: 1, WorkerCount: 1})
	slow := newBlockingTransport()
	d.Register("table1", newTestSink(slow))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	// dispatch1 occupies the lone worker (blocks in Send; queue now empty).
	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "table1", record) }()
	<-slow.entered

	// dispatch2 fills the queue (worker is blocked, so this job stays queued).
	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "table1", record) }()
	time.Sleep(100 * time.Millisecond)

	// dispatch3 blocks on the full queue: this is the backpressured submit that
	// must not hold close hostage.
	f3 := make(chan error, 1)
	go func() { f3 <- d.Dispatch(ctx, "table1", record) }()
	time.Sleep(100 * time.Millisecond)

	// Close must unblock the backpressured submit even while the worker is still
	// blocked; before the fix this deadlocked (close waited on stateMu while the
	// submit held it waiting for queue capacity).
	closeDone := make(chan error, 1)
	go func() { closeDone <- d.Close() }()

	// The backpressured submit must be rejected with errLaneClosed while the
	// worker is still blocked, proving close (not the freed worker) unblocked it.
	select {
	case err := <-f3:
		assert.ErrorIs(t, err, errLaneClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("backpressured submit never settled after Close")
	}

	// Release the worker so Close can finish draining the in-flight job.
	close(slow.gate)
	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the worker was released")
	}
	<-f1
	<-f2
}

// TestDispatcherCloseOrphanRace guards the two-phase close protocol against the
// orphan-job race: a submit that wins the send race after close begins must
// still be drained and produce a result, never hang. Close rejects new submits
// (phase 1) but only signals workers to drain/exit (phase 2) after every
// in-flight submit has settled, so an accepted job is always delivered. The
// test hammers concurrent Dispatch calls against a Close and asserts every
// Dispatch returns (nil or errLaneClosed) and Close returns.
func TestDispatcherCloseOrphanRace(t *testing.T) {
	d := NewDispatcherWithConfig(Config{QueueSize: 4, WorkerCount: 2})
	transport := &atomicTransport{}
	d.Register("table1", NewRuntimeSink(collections.Sink{ID: "s1"}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	const dispatchers = 8
	const perDispatcher = 200

	results := make(chan error, dispatchers*perDispatcher)
	var wg sync.WaitGroup
	for i := 0; i < dispatchers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perDispatcher; j++ {
				results <- d.Dispatch(ctx, "table1", record)
			}
		}()
	}

	// Close while dispatches are racing. Every accepted job must be drained and
	// every rejected submit must return errLaneClosed; nothing may hang.
	if err := d.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	wg.Wait()
	close(results)

	for err := range results {
		if err != nil && !errors.Is(err, errLaneClosed) {
			t.Fatalf("Dispatch returned unexpected error: %v", err)
		}
	}
}

// TestDispatcherRemoveWhileBackpressured is the Remove variant of the close
// deadlock test: removing a sink whose lane queue is full and has a blocked
// submit must not deadlock, and the blocked submit must be rejected.
func TestDispatcherRemoveWhileBackpressured(t *testing.T) {
	d := NewDispatcherWithConfig(Config{QueueSize: 1, WorkerCount: 1})
	slow := newBlockingTransport()
	sink := newTestSink(slow)
	d.Register("table1", sink)

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "table1", record) }()
	<-slow.entered

	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "table1", record) }()
	time.Sleep(100 * time.Millisecond)

	f3 := make(chan error, 1)
	go func() { f3 <- d.Dispatch(ctx, "table1", record) }()
	time.Sleep(100 * time.Millisecond)

	removeDone := make(chan struct{})
	go func() {
		defer close(removeDone)
		d.Remove("table1", sink.Key())
	}()

	// The backpressured submit must be rejected while the worker is still
	// blocked, proving Remove (not the freed worker) unblocked it.
	select {
	case err := <-f3:
		assert.ErrorIs(t, err, errLaneClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("backpressured submit never settled after Remove")
	}

	// Release the worker so Remove can finish draining the in-flight job.
	close(slow.gate)
	select {
	case <-removeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Remove did not return after the worker was released")
	}
	<-f1
	<-f2
}

// TestBuildTransportFailClosed verifies that a configured sink whose transport
// cannot be built at runtime (unknown type, or a builder that rejects the spec)
// yields a non-nil erroring transport rather than nil, so the sink is never
// silently skipped.
func TestBuildTransportFailClosed(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown sink type returns erroring transport", func(t *testing.T) {
		tr := BuildTransport(ctx, "users", collections.Type("kafka"), map[string]interface{}{"endpoint": "http://localhost:3000"})
		require.NotNil(t, tr, "unknown type must not return nil")
		err := tr.Send(ctx, streams.StreamRecord{RecordType: streams.InsertRecord})
		require.Error(t, err)
		assert.ErrorIs(t, err, errUnavailable)
	})

	t.Run("builder rejecting spec returns erroring transport", func(t *testing.T) {
		// Register a unique test-only builder that rejects its spec by
		// returning nil, so the subtest exercises the registered-builder
		// path rather than the "no registered builder" path.
		testType := collections.Type("test-rejecting-builder")
		RegisterTransport(testType, func(ctx context.Context, collectionName string, t collections.Type, spec map[string]interface{}) Transport {
			return nil
		})
		defer delete(transportBuilders, testType)

		tr := BuildTransport(ctx, "users", testType, map[string]interface{}{})
		require.NotNil(t, tr, "rejected spec must not return nil")
		err := tr.Send(ctx, streams.StreamRecord{RecordType: streams.InsertRecord})
		require.Error(t, err)
		assert.ErrorIs(t, err, errUnavailable)
	})
}

// TestDispatcherUnavailableLaneFailClosed proves a configured sink whose
// transport could not be built still participates in dispatch: Dispatch returns
// an error for matching events (so the watcher does not advance the resume token
// and routes to retry) instead of silently settling them.
func TestDispatcherUnavailableLaneFailClosed(t *testing.T) {
	ctx := context.Background()
	d := NewDispatcher()

	tr := newUnavailableTransport(errors.New("no transport registered for sink type"))
	require.NotNil(t, tr)
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1"}, tr))

	// A matching event must fail dispatch, not be silently acknowledged.
	err := d.Dispatch(ctx, "users", streams.StreamRecord{RecordType: streams.InsertRecord})
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnavailable)
}

// TestDispatcherZeroSinksStillSucceeds proves a collection with no configured
// sinks continues to Dispatch()==nil; the legitimate zero-sink case is not a
// failure.
func TestDispatcherZeroSinksStillSucceeds(t *testing.T) {
	d := NewDispatcher()
	ctx := context.Background()
	err := d.Dispatch(ctx, "users", streams.StreamRecord{RecordType: streams.InsertRecord})
	assert.NoError(t, err)
}
