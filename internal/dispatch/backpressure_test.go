package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/metrics"
	"conduit/internal/streams"
)

// TestSinkQueueCapacitySetAtRegistration proves the lane's configured capacity
// is reported exactly once when the lane is created and never updated per event,
// and that removing the lane triggers a full lane-metrics deletion.
func TestSinkQueueCapacitySetAtRegistration(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 7, WorkerCount: 2}, nil, rec, nil)
	sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{})
	d.Register("users", sink)

	// Exactly one capacity observation, with the configured queue size and the
	// correct labels.
	assert.Len(t, rec.capCalls(), 1, "capacity must be reported exactly once at registration")
	cap, ok := rec.capacityFor("users", "s1")
	require.True(t, ok, "a capacity observation must exist")
	assert.Equal(t, 7, cap, "capacity must equal the configured queue size")

	// Dispatch several events: capacity is a gauge set once at lane creation,
	// never updated per event.
	for i := 0; i < 3; i++ {
		require.NoError(t, d.Dispatch(context.Background(), "users",
			streams.StreamRecord{RecordType: streams.InsertRecord}))
	}
	assert.Len(t, rec.capCalls(), 1, "capacity must never be updated per event")

	// Removing the lane deletes all its per-lane series exactly once.
	require.NoError(t, d.Dispatch(context.Background(), "users",
		streams.StreamRecord{RecordType: streams.InsertRecord}))
	d.Remove("users", sink.Key())
	assert.Equal(t, []delCall{{collection: "users", sinkType: collections.SinkTypeHTTP, sinkID: "s1"}},
		rec.deleteCalls())
}

// TestEnqueueWaitImmediateCapacity records small waits with available capacity
// and no queue-full counts.
func TestEnqueueWaitImmediateCapacity(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 8, WorkerCount: 4}, nil, rec, nil)
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, &MockTransport{}))

	for i := 0; i < 4; i++ {
		require.NoError(t, d.Dispatch(context.Background(), "users",
			streams.StreamRecord{RecordType: streams.InsertRecord}))
	}

	waits := rec.waitsFor("users", "s1")
	require.Len(t, waits, 4, "each enqueued event must record an enqueue wait")
	for _, w := range waits {
		assert.GreaterOrEqual(t, w, time.Duration(0), "waits can never be negative")
		assert.Less(t, w, 100*time.Millisecond, "an immediately-available queue must produce a near-zero wait")
	}
	assert.Zero(t, rec.fullCount("users", "s1"), "no enqueue should hit a full queue")
}

// TestEnqueueWaitDurationBlockedEnqueue proves a blocked enqueue records a
// non-trivial wait duration while the fast-path events record near-zero waits.
func TestEnqueueWaitDurationBlockedEnqueue(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 1, WorkerCount: 1}, nil, rec, nil)
	transport := newGatedTransport()
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	// Event 1 occupies the lone worker (blocks in Send; queue empty).
	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "users", record) }()
	// Event 2 fills the queue (depth 1 observed by the worker blocked on event 1).
	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "users", record) }()
	require.Eventually(t, func() bool {
		depth, ok := rec.lastDepth("users", "s1")
		return ok && depth == 1
	}, 5*time.Second, time.Millisecond, "the full queue must report depth 1")

	// Event 3 blocks on the full queue: its Dispatch must not return while the
	// queue is full.
	f3 := make(chan error, 1)
	go func() { f3 <- d.Dispatch(ctx, "users", record) }()
	select {
	case err := <-f3:
		t.Fatalf("third dispatch returned %v while the lane was full; expected backpressure", err)
	default:
	}

	// Hold the third enqueue blocked for a measurable window, then release.
	time.Sleep(200 * time.Millisecond)
	close(transport.release)

	require.NoError(t, <-f1)
	require.NoError(t, <-f2)
	require.NoError(t, <-f3)

	waits := rec.waitsFor("users", "s1")
	require.Len(t, waits, 3, "each of the three enqueued events must record a wait")

	// Events 1-2 are fast-path enqueues: near-zero waits. The blocked event 3 is
	// enqueued only after the gate closes, so its wait must be >= the hold window.
	fast, blocked := 0, 0
	for _, w := range waits {
		if w >= 100*time.Millisecond {
			blocked++
		} else {
			fast++
		}
	}
	assert.GreaterOrEqual(t, blocked, 1, "the blocked third enqueue must record a wait >= 100ms")
	assert.GreaterOrEqual(t, fast, 2, "the two fast-path enqueues must record waits < 100ms")

	// At least one queue-full was recorded (the blocked third enqueue).
	assert.GreaterOrEqual(t, rec.fullCount("users", "s1"), 1)
}

// TestQueueFullCounterOncePerEnqueue proves the queue-full counter is
// incremented exactly once per blocked enqueue, not repeatedly while it stays
// blocked.
func TestQueueFullCounterOncePerEnqueue(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 1, WorkerCount: 1}, nil, rec, nil)
	transport := newGatedTransport()
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "users", record) }()
	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "users", record) }()
	require.Eventually(t, func() bool {
		depth, ok := rec.lastDepth("users", "s1")
		return ok && depth == 1
	}, 5*time.Second, time.Millisecond, "the full queue must report depth 1")

	f3 := make(chan error, 1)
	go func() { f3 <- d.Dispatch(ctx, "users", record) }()
	time.Sleep(200 * time.Millisecond)

	close(transport.release)
	require.NoError(t, <-f1)
	require.NoError(t, <-f2)
	require.NoError(t, <-f3)

	// Exactly one enqueue encountered the full queue — the counter is NOT
	// incremented repeatedly while the enqueue stays blocked.
	assert.Equal(t, 1, rec.fullCount("users", "s1"),
		"queue-full must be counted once per blocked enqueue, not per poll")
}

// TestSinkLaneMetricsLifecycleWithRealRegistry drives a real metrics registry
// through the full lane lifecycle and proves all four backpressure families get
// series while the lane is active and are wiped from Gather after removal.
func TestSinkLaneMetricsLifecycleWithRealRegistry(t *testing.T) {
	m := metrics.New()
	d := NewDispatcher(Config{QueueSize: 1, WorkerCount: 1}, m, m, nil)
	transport := newGatedTransport()
	sink := NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport)
	d.Register("users", sink)

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	f1 := make(chan error, 1)
	go func() { f1 <- d.Dispatch(ctx, "users", record) }()
	f2 := make(chan error, 1)
	go func() { f2 <- d.Dispatch(ctx, "users", record) }()
	require.Eventually(t, func() bool {
		_, ok := backpressureGaugeValue(t, m, "conduit_sink_queue_depth", "collection", "users", "sink_type", "http", "sink_id", "s1")
		return ok
	}, 5*time.Second, time.Millisecond, "a queue-depth series must appear")

	f3 := make(chan error, 1)
	go func() { f3 <- d.Dispatch(ctx, "users", record) }()
	time.Sleep(200 * time.Millisecond)
	close(transport.release)
	require.NoError(t, <-f1)
	require.NoError(t, <-f2)
	require.NoError(t, <-f3)

	labels := map[string]string{
		"collection": "users",
		"sink_type":  "http",
		"sink_id":    "s1",
	}
	// All four families present, each with exactly one series for the lane.
	for _, name := range []string{
		"conduit_sink_queue_depth",
		"conduit_sink_queue_capacity",
		"conduit_sink_enqueue_wait_duration_seconds",
		"conduit_sink_queue_full_total",
	} {
		assert.Equal(t, 1, backpressureSeriesCount(t, m, name, labels), "family %s must have exactly one series", name)
	}
	// Capacity reflects the configured queue size of 1.
	cap, ok := backpressureGaugeValue(t, m, "conduit_sink_queue_capacity", "collection", "users", "sink_type", "http", "sink_id", "s1")
	require.True(t, ok)
	assert.Equal(t, 1.0, cap)
	// The enqueue-wait histogram accumulated the three enqueues (including the
	// blocked third one).
	assert.GreaterOrEqual(t, backpressureHistogramCount(t, m, "conduit_sink_enqueue_wait_duration_seconds", labels), uint64(3))

	// Remove the lane; all four families must vanish from Gather (a Vec with
	// zero series is omitted).
	d.Remove("users", sink.Key())
	gathered, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range gathered {
		assert.NotContains(t, []string{
			"conduit_sink_queue_depth",
			"conduit_sink_queue_capacity",
			"conduit_sink_enqueue_wait_duration_seconds",
			"conduit_sink_queue_full_total",
		}, mf.GetName(), "family %s must have no series after removal", mf.GetName())
	}
}

// TestSinkBackpressureConcurrent is a race-safety sweep with 50 concurrent
// dispatch goroutines, then a Close, asserting the lane's series are deleted
// exactly once and no negative waits are recorded.
func TestSinkBackpressureConcurrent(t *testing.T) {
	rec := &queueDepthRecorder{}
	d := NewDispatcher(Config{QueueSize: 2, WorkerCount: 1}, nil, rec, nil)
	transport := &concurrentTransport{}
	d.Register("users", NewRuntimeSink(collections.Sink{ID: "s1", Type: collections.SinkTypeHTTP}, transport))

	ctx := context.Background()
	record := streams.StreamRecord{RecordType: streams.InsertRecord}

	synchronized := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-synchronized
			_ = d.Dispatch(ctx, "users", record)
		}()
	}
	close(synchronized)
	wg.Wait()

	waits := rec.waitsFor("users", "s1")
	for _, w := range waits {
		assert.GreaterOrEqual(t, w, time.Duration(0), "waits can never be negative")
	}
	assert.GreaterOrEqual(t, rec.fullCount("users", "s1"), 0)

	require.NoError(t, d.Close())
	assert.Len(t, rec.deleteCalls(), 1, "closing must delete the single lane's series exactly once")
}

// backpressureGaugeValue reads the current gauge value of a family for the given
// exact label pair set, returning the value and whether the series exists.
func backpressureGaugeValue(t *testing.T, m *metrics.Metrics, family string, labels ...string) (float64, bool) {
	t.Helper()
	require.Equal(t, 0, len(labels)%2, "labels must be key/value pairs")
	need := make(map[string]string, len(labels)/2)
	for i := 0; i < len(labels); i += 2 {
		need[labels[i]] = labels[i+1]
	}
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != family {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if !matchingLabels(metric, need) {
				continue
			}
			if g := metric.GetGauge(); g != nil {
				return g.GetValue(), true
			}
		}
	}
	return 0.0, false
}

// backpressureSeriesCount returns the number of series in a family carrying at
// least the given label subset.
func backpressureSeriesCount(t *testing.T, m *metrics.Metrics, family string, want map[string]string) int {
	t.Helper()
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	count := 0
	for _, mf := range families {
		if mf.GetName() != family {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if matchingLabels(metric, want) {
				count++
			}
		}
	}
	return count
}

// backpressureHistogramCount returns the sum of SampleCount across all series of
// a histogram family carrying at least the given label subset.
func backpressureHistogramCount(t *testing.T, m *metrics.Metrics, family string, want map[string]string) uint64 {
	t.Helper()
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	var total uint64
	for _, mf := range families {
		if mf.GetName() != family {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if matchingLabels(metric, want) {
				total += metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return total
}

// matchingLabels reports whether a gathered metric carries at least the given
// label subset.
func matchingLabels(metric *dto.Metric, want map[string]string) bool {
	labels := map[string]string{}
	for _, l := range metric.GetLabel() {
		labels[l.GetName()] = l.GetValue()
	}
	for k, v := range want {
		if labels[k] != v {
			return false
		}
	}
	return true
}
