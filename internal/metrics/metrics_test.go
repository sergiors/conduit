package metrics

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/streams"
)

// TestRegistration verifies a fresh New() registers every expected metric family
// on its own registry, and that two instances are fully independent (the point
// of the dedicated registry — no global duplicate-registration collisions).
func TestRegistration(t *testing.T) {
	m := New()
	require.NotNil(t, m.registry)

	// Seed each family with one observation so Gather() surfaces them (a fresh
	// vector with no series is omitted from Gather output).
	m.ObserveEventsProcessed("users", streams.InsertRecord)
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	m.sinkDeliveryDuration.WithLabelValues("users", string(collections.SinkTypeHTTP)).Observe(0.01)
	m.SetWatcherRunning("users", true)
	m.SetRetryQueueDepth("users", 1)
	m.SetDLQEntries("users", 1)
	m.ObserveSinkQueueDepth("users", collections.SinkTypeHTTP, "s1", 3)
	m.SetSinkQueueCapacity("users", collections.SinkTypeHTTP, "s1", 1024)
	m.ObserveSinkEnqueueWait("users", collections.SinkTypeHTTP, "s1", 5*time.Millisecond)
	m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s1")

	gathered, err := m.registry.Gather()
	require.NoError(t, err)

	// Every metric family must be present on the registry.
	names := map[string]bool{}
	for _, mf := range gathered {
		names[mf.GetName()] = true
	}
	for _, name := range []string{
		MetricEventsProcessedTotal,
		MetricSinkDeliveriesTotal,
		MetricSinkDeliveryDurationSeconds,
		MetricWatcherRunning,
		MetricRetryQueueDepth,
		MetricSinkQueueDepth,
		MetricSinkQueueCapacity,
		MetricSinkEnqueueWaitDurationSeconds,
		MetricSinkQueueFullTotal,
		MetricDLQEntries,
	} {
		assert.True(t, names[name], "metric family %s must be registered", name)
	}

	// Two independent instances must not collide (no global registry).
	m2 := New()
	require.NotNil(t, m2.registry)
	assert.NotSame(t, m.registry, m2.registry)

	// Registering into both and gathering both separately must not panic or
	// report duplicate registration errors.
	_, err = m.registry.Gather()
	require.NoError(t, err)
	_, err = m2.registry.Gather()
	require.NoError(t, err)
}

// TestEventsProcessedCounter verifies the events-processed counter accumulates
// per (collection, event_type) with the canonical string record types, and that
// an empty event type is skipped defensively.
func TestEventsProcessedCounter(t *testing.T) {
	m := New()

	m.ObserveEventsProcessed("users", streams.InsertRecord)
	m.ObserveEventsProcessed("users", streams.InsertRecord)
	m.ObserveEventsProcessed("users", streams.ModifyRecord)
	m.ObserveEventsProcessed("orders", streams.RemoveRecord)

	assert.Equal(t, 2.0, testutil.ToFloat64(m.eventsProcessed.WithLabelValues("users", string(streams.InsertRecord))))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.eventsProcessed.WithLabelValues("users", string(streams.ModifyRecord))))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.eventsProcessed.WithLabelValues("orders", string(streams.RemoveRecord))))

	// An empty event type is skipped defensively (no empty-label series).
	m.ObserveEventsProcessed("users", "")
	assert.Equal(t, 2.0, testutil.ToFloat64(m.eventsProcessed.WithLabelValues("users", string(streams.InsertRecord))),
		"empty event type must not increment the series")
	assert.Equal(t, 0.0, testutil.ToFloat64(m.eventsProcessed.WithLabelValues("users", "")))
}

// TestSinkDeliveries verifies the outcome label stays bounded to
// success/failure and that success/failure produce distinct series.
func TestSinkDeliveries(t *testing.T) {
	m := New()

	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, assert.AnError)

	m.ObserveSinkDelivery("orders", collections.SinkTypeEventBridge, 0, assert.AnError)

	success := m.sinkDeliveries.WithLabelValues("users", string(collections.SinkTypeHTTP), string(OutcomeSuccess))
	failure := m.sinkDeliveries.WithLabelValues("users", string(collections.SinkTypeHTTP), string(OutcomeFailure))
	require.NotNil(t, success)
	require.NotNil(t, failure)

	assert.Equal(t, 2.0, testutil.ToFloat64(success))
	assert.Equal(t, 1.0, testutil.ToFloat64(failure))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.sinkDeliveries.WithLabelValues("orders", string(collections.SinkTypeEventBridge), string(OutcomeFailure))))
	assert.Zero(t, testutil.ToFloat64(m.sinkDeliveries.WithLabelValues("orders", string(collections.SinkTypeEventBridge), string(OutcomeSuccess))))

	// The outcome label cardinality is exactly the two bounded values.
	gathered, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range gathered {
		if mf.GetName() != MetricSinkDeliveriesTotal {
			continue
		}
		outcomes := map[string]bool{}
		for _, metric := range mf.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == outcomeLabel {
					outcomes[l.GetValue()] = true
				}
			}
		}
		assert.Equal(t, 2, len(outcomes), "outcome label must be bounded to success/failure")
		assert.Contains(t, outcomes, string(OutcomeSuccess))
		assert.Contains(t, outcomes, string(OutcomeFailure))
	}
}

// TestSinkDeliveryDurationHistogram verifies the duration histogram records
// observations and produces the correct per-series sample count.
func TestSinkDeliveryDurationHistogram(t *testing.T) {
	m := New()

	h := m.sinkDeliveryDuration.WithLabelValues("users", string(collections.SinkTypeHTTP))
	require.NotNil(t, h)

	h.Observe(0.01)
	h.Observe(0.02)
	h.Observe(0.03)

	// Sum the sample counts across the matching histogram family to assert N
	// observations were recorded.
	total := totalHistogramCount(t, m, MetricSinkDeliveryDurationSeconds, map[string]string{
		collectionLabel: "users",
		sinkTypeLabel:   string(collections.SinkTypeHTTP),
	})
	assert.Equal(t, uint64(3), total, "histogram must record 3 observations")
}

// totalHistogramCount sums the SampleCount of all histogram series that carry
// at least the given label subset.
func totalHistogramCount(t *testing.T, m *Metrics, name string, wantLabels map[string]string) uint64 {
	t.Helper()
	gathered, err := m.Registry().Gather()
	require.NoError(t, err)

	var total uint64
	for _, mf := range gathered {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range metric.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			matched := true
			for k, v := range wantLabels {
				if labels[k] != v {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			total += metric.GetHistogram().GetSampleCount()
		}
	}
	return total
}

// TestGauges verifies exact values for the gauge families.
func TestGauges(t *testing.T) {
	m := New()

	m.SetWatcherRunning("users", true)
	m.SetWatcherRunning("orders", false)
	assert.Equal(t, 1.0, testutil.ToFloat64(m.watcherRunning.WithLabelValues("users")))
	assert.Equal(t, 0.0, testutil.ToFloat64(m.watcherRunning.WithLabelValues("orders")))

	// Flip users back to not-running.
	m.SetWatcherRunning("users", false)
	assert.Equal(t, 0.0, testutil.ToFloat64(m.watcherRunning.WithLabelValues("users")))

	m.SetRetryQueueDepth("users", 42)
	assert.Equal(t, 42.0, testutil.ToFloat64(m.retryQueueDepth.WithLabelValues("users")))

	m.SetDLQEntries("users", 7)
	assert.Equal(t, 7.0, testutil.ToFloat64(m.dlqEntries.WithLabelValues("users")))
}

// TestSinkQueueDepth verifies the sink queue depth gauge records per
// (collection, sink_type, sink_id), overwrites on re-set, keeps the label set
// exactly {collection, sink_type, sink_id}, and that a delete removes the
// series (DeleteLabelValues returns true once then false).
func TestSinkQueueDepth(t *testing.T) {
	m := New()

	m.ObserveSinkQueueDepth("users", collections.SinkTypeHTTP, "s1", 3)
	assert.Equal(t, 3.0, testutil.ToFloat64(
		m.sinkQueueDepth.WithLabelValues("users", string(collections.SinkTypeHTTP), "s1")))
	assert.Equal(t, 0.0, testutil.ToFloat64(
		m.sinkQueueDepth.WithLabelValues("users", string(collections.SinkTypeHTTP), "s2")),
		"a distinct sink id must be a distinct series")

	// The gauge overwrites rather than accumulates (it is a gauge, not a counter).
	m.ObserveSinkQueueDepth("users", collections.SinkTypeHTTP, "s1", 5)
	m.ObserveSinkQueueDepth("users", collections.SinkTypeHTTP, "s1", 2)
	assert.Equal(t, 2.0, testutil.ToFloat64(
		m.sinkQueueDepth.WithLabelValues("users", string(collections.SinkTypeHTTP), "s1")))

	// Assert the exact label set (names + values) via a gathered metric slice.
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != MetricSinkQueueDepth {
			continue
		}
		metric := mf.GetMetric()[0]
		labels := map[string]string{}
		for _, l := range metric.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		assert.Equal(t, map[string]string{
			collectionLabel: "users",
			sinkTypeLabel:   string(collections.SinkTypeHTTP),
			sinkIDLabel:     "s1",
		}, labels, "the sink queue depth gauge must expose exactly collection/sink_type/sink_id")
	}

	// Delete removes the series: DeleteLabelValues reports true once, then false.
	assert.True(t, m.sinkQueueDepth.DeleteLabelValues("users", string(collections.SinkTypeHTTP), "s1"))
	assert.False(t, m.sinkQueueDepth.DeleteLabelValues("users", string(collections.SinkTypeHTTP), "s1"),
		"deleting a removed series must report false")
}

// TestSinkQueueCapacity verifies the capacity gauge records per
// (collection, sink_type, sink_id) as an overwriting gauge (not an accumulator),
// keeps the label set exactly {collection, sink_type, sink_id}, and that a
// distinct sink id is a distinct series.
func TestSinkQueueCapacity(t *testing.T) {
	m := New()

	m.SetSinkQueueCapacity("users", collections.SinkTypeHTTP, "s1", 128)
	assert.Equal(t, 128.0, testutil.ToFloat64(
		m.sinkQueueCapacity.WithLabelValues("users", string(collections.SinkTypeHTTP), "s1")))
	assert.Equal(t, 0.0, testutil.ToFloat64(
		m.sinkQueueCapacity.WithLabelValues("users", string(collections.SinkTypeHTTP), "s2")),
		"a distinct sink id must be a distinct series")

	// The gauge overwrites rather than accumulates.
	m.SetSinkQueueCapacity("users", collections.SinkTypeHTTP, "s1", 512)
	m.SetSinkQueueCapacity("users", collections.SinkTypeHTTP, "s1", 64)
	assert.Equal(t, 64.0, testutil.ToFloat64(
		m.sinkQueueCapacity.WithLabelValues("users", string(collections.SinkTypeHTTP), "s1")),
		"capacity is a gauge, not an accumulator")

	// Assert the exact label set via a gathered metric slice.
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != MetricSinkQueueCapacity {
			continue
		}
		metric := mf.GetMetric()[0]
		labels := map[string]string{}
		for _, l := range metric.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		assert.Equal(t, map[string]string{
			collectionLabel: "users",
			sinkTypeLabel:   string(collections.SinkTypeHTTP),
			sinkIDLabel:     "s1",
		}, labels, "the sink queue capacity gauge must expose exactly collection/sink_type/sink_id")
	}
}

// TestSinkEnqueueWaitHistogram verifies the enqueue-wait histogram records
// observations, reports a positive sum, and exposes the sink_id label.
func TestSinkEnqueueWaitHistogram(t *testing.T) {
	m := New()

	m.ObserveSinkEnqueueWait("users", collections.SinkTypeHTTP, "s1", 5*time.Millisecond)
	m.ObserveSinkEnqueueWait("users", collections.SinkTypeHTTP, "s1", 10*time.Millisecond)
	m.ObserveSinkEnqueueWait("users", collections.SinkTypeHTTP, "s1", 20*time.Millisecond)

	total := totalHistogramCount(t, m, MetricSinkEnqueueWaitDurationSeconds, map[string]string{
		collectionLabel: "users",
		sinkTypeLabel:   string(collections.SinkTypeHTTP),
		sinkIDLabel:     "s1",
	})
	assert.Equal(t, uint64(3), total, "histogram must record 3 observations")

	families, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != MetricSinkEnqueueWaitDurationSeconds {
			continue
		}
		metric := mf.GetMetric()[0]
		assert.Greater(t, metric.GetHistogram().GetSampleSum(), 0.0, "sum of recorded waits must be positive")
		labels := map[string]string{}
		for _, l := range metric.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		assert.Contains(t, labels, sinkIDLabel, "the enqueue-wait histogram must expose sink_id")
		assert.Equal(t, map[string]string{
			collectionLabel: "users",
			sinkTypeLabel:   string(collections.SinkTypeHTTP),
			sinkIDLabel:     "s1",
		}, labels, "the enqueue-wait histogram must expose exactly collection/sink_type/sink_id")
	}
}

// TestSinkQueueFullCounter verifies the queue-full counter accumulates across
// calls and records distinct per-sink-id series.
func TestSinkQueueFullCounter(t *testing.T) {
	m := New()

	m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s1")
	m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s1")
	m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s1")
	assert.Equal(t, 3.0, testutil.ToFloat64(
		m.sinkQueueFull.WithLabelValues("users", string(collections.SinkTypeHTTP), "s1")),
		"queue-full is a counter, accumulating across calls")

	m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s2")
	assert.Equal(t, 1.0, testutil.ToFloat64(
		m.sinkQueueFull.WithLabelValues("users", string(collections.SinkTypeHTTP), "s2")),
		"a distinct sink id must be a distinct series")
	assert.Equal(t, 0.0, testutil.ToFloat64(
		m.sinkQueueFull.WithLabelValues("orders", string(collections.SinkTypeHTTP), "s1")),
		"a distinct collection must be a distinct series")
}

// TestDeleteSinkLaneMetrics verifies deleting a lane removes every per-lane
// series across all four backpressure families, each underlying Vec reports a
// successful first delete then false, and a second delete of an already-removed
// lane leaves Gather unchanged.
func TestDeleteSinkLaneMetrics(t *testing.T) {
	m := New()
	labels := func() map[string]string {
		return map[string]string{
			collectionLabel: "users",
			sinkTypeLabel:   string(collections.SinkTypeHTTP),
			sinkIDLabel:     "s1",
		}
	}

	// Seed all four families for one lane.
	m.ObserveSinkQueueDepth("users", collections.SinkTypeHTTP, "s1", 3)
	m.SetSinkQueueCapacity("users", collections.SinkTypeHTTP, "s1", 1024)
	m.ObserveSinkEnqueueWait("users", collections.SinkTypeHTTP, "s1", 5*time.Millisecond)
	m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s1")

	// Every family must be present before deletion.
	for _, name := range []string{MetricSinkQueueDepth, MetricSinkQueueCapacity, MetricSinkEnqueueWaitDurationSeconds, MetricSinkQueueFullTotal} {
		require.True(t, familyHasSeries(t, m, name, labels()), "family %s must be seeded", name)
	}

	m.DeleteSinkLaneMetrics("users", collections.SinkTypeHTTP, "s1")

	// No family should expose the lane's series after deletion.
	for _, name := range []string{MetricSinkQueueDepth, MetricSinkQueueCapacity, MetricSinkEnqueueWaitDurationSeconds, MetricSinkQueueFullTotal} {
		assert.False(t, familyHasSeries(t, m, name, labels()), "family %s must have no series after deletion", name)
	}

	// Each underlying Vec reports no series present (it was already removed by
	// DeleteSinkLaneMetrics, so a direct DeleteLabelValues reports false).
	assert.False(t, m.sinkQueueDepth.DeleteLabelValues("users", string(collections.SinkTypeHTTP), "s1"))
	assert.False(t, m.sinkQueueCapacity.DeleteLabelValues("users", string(collections.SinkTypeHTTP), "s1"))
	assert.False(t, m.sinkEnqueueWait.DeleteLabelValues("users", string(collections.SinkTypeHTTP), "s1"))
	assert.False(t, m.sinkQueueFull.DeleteLabelValues("users", string(collections.SinkTypeHTTP), "s1"))

	// A second DeleteSinkLaneMetrics leaves Gather unchanged (still no series).
	before, err := m.Registry().Gather()
	require.NoError(t, err)
	m.DeleteSinkLaneMetrics("users", collections.SinkTypeHTTP, "s1")
	after, err := m.Registry().Gather()
	require.NoError(t, err)
	assert.Len(t, after, len(before), "a second delete of an already-removed lane must not change the registry")
}

// familyHasSeries reports whether the given family exposes a series carrying at
// least the given label subset.
func familyHasSeries(t *testing.T, m *Metrics, name string, wantLabels map[string]string) bool {
	t.Helper()
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range metric.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			matched := true
			for k, v := range wantLabels {
				if labels[k] != v {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}

// TestNilSafety verifies every method is a no-op (no panic) on a nil receiver.
func TestNilSafety(t *testing.T) {
	var m *Metrics
	require.NotPanics(t, func() {
		m.ObserveEventsProcessed("users", streams.InsertRecord)
		m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
		m.SetWatcherRunning("users", true)
		m.SetRetryQueueDepth("users", 1)
		m.SetDLQEntries("users", 1)
		m.ObserveSinkQueueDepth("users", collections.SinkTypeHTTP, "s1", 1)
		m.SetSinkQueueCapacity("users", collections.SinkTypeHTTP, "s1", 1024)
		m.ObserveSinkEnqueueWait("users", collections.SinkTypeHTTP, "s1", time.Millisecond)
		m.IncSinkQueueFull("users", collections.SinkTypeHTTP, "s1")
		m.DeleteSinkLaneMetrics("users", collections.SinkTypeHTTP, "s1")
		m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, assert.AnError)
	})
	assert.Nil(t, m.Registry())
	assert.NotPanics(t, func() { _ = m.Handler() })
}

// TestHandlerServesPrometheusText verifies the handler produces the Prometheus
// exposition format.
func TestHandlerServesPrometheusText(t *testing.T) {
	m := New()
	m.SetWatcherRunning("users", true)
	handler := m.Handler()

	// The handler is promhttp-generated; writing through it must produce text.
	rec := &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(rec, mustRequest(t, "/metrics"))

	body := rec.body.String()
	assert.Contains(t, body, MetricWatcherRunning)
	assert.True(t, strings.HasPrefix(rec.header.Get("Content-Type"), "text/plain; version=0.0.4"),
		"expected Prometheus exposition content type, got %q", rec.header.Get("Content-Type"))
}

type responseRecorder struct {
	header http.Header
	body   strings.Builder
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) {
	r.status = 200
	return r.body.Write(b)
}
func (r *responseRecorder) WriteHeader(code int) { r.status = code }

func mustRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("GET", target, nil)
	require.NoError(t, err)
	return req
}
