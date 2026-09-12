package metrics

import (
	"net/http"
	"strings"
	"testing"

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
	m.SinkDeliveryDuration("users", collections.SinkTypeHTTP).Observe(0.01)
	m.SetWatcherRunning("users", true)
	m.SetRetryQueueDepth("users", 1)
	m.SetDLQEntries("users", 1)

	gathered, err := m.registry.Gather()
	require.NoError(t, err)

	// Every metric family must be present on the registry.
	names := map[string]bool{}
	for _, mf := range gathered {
		names[mf.GetName()] = true
	}
	for _, name := range []string{
		eventsProcessedName,
		sinkDeliveriesName,
		sinkDeliveryDurationName,
		watcherRunningName,
		retryQueueDepthName,
		dlqEntriesName,
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

	assert.Equal(t, 2.0, testutil.ToFloat64(m.EventsProcessedTotal("users", string(streams.InsertRecord))))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.EventsProcessedTotal("users", string(streams.ModifyRecord))))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.EventsProcessedTotal("orders", string(streams.RemoveRecord))))

	// An empty event type is skipped defensively (no empty-label series).
	m.ObserveEventsProcessed("users", "")
	assert.Equal(t, 2.0, testutil.ToFloat64(m.EventsProcessedTotal("users", string(streams.InsertRecord))),
		"empty event type must not increment the series")
	assert.Equal(t, 0.0, testutil.ToFloat64(m.EventsProcessedTotal("users", "")))
}

// TestSinkDeliveries verifies the outcome label stays bounded to
// success/failure and that success/failure produce distinct series.
func TestSinkDeliveries(t *testing.T) {
	m := New()

	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, assert.AnError)

	m.ObserveSinkDelivery("orders", collections.SinkTypeEventBridge, 0, assert.AnError)

	success := m.SinkDeliveries("users", collections.SinkTypeHTTP, OutcomeSuccess)
	failure := m.SinkDeliveries("users", collections.SinkTypeHTTP, OutcomeFailure)
	require.NotNil(t, success)
	require.NotNil(t, failure)

	assert.Equal(t, 2.0, testutil.ToFloat64(success))
	assert.Equal(t, 1.0, testutil.ToFloat64(failure))
	assert.Equal(t, 1.0, testutil.ToFloat64(m.SinkDeliveries("orders", collections.SinkTypeEventBridge, OutcomeFailure)))
	assert.Zero(t, testutil.ToFloat64(m.SinkDeliveries("orders", collections.SinkTypeEventBridge, OutcomeSuccess)))

	// The outcome label cardinality is exactly the two bounded values.
	gathered, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, mf := range gathered {
		if mf.GetName() != sinkDeliveriesName {
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

	h := m.SinkDeliveryDuration("users", collections.SinkTypeHTTP)
	require.NotNil(t, h)

	h.Observe(0.01)
	h.Observe(0.02)
	h.Observe(0.03)

	// Sum the sample counts across the matching histogram family to assert N
	// observations were recorded.
	total := totalHistogramCount(t, m, sinkDeliveryDurationName, map[string]string{
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
	assert.Equal(t, 1.0, testutil.ToFloat64(m.WatcherRunning("users")))
	assert.Equal(t, 0.0, testutil.ToFloat64(m.WatcherRunning("orders")))

	// Flip users back to not-running.
	m.SetWatcherRunning("users", false)
	assert.Equal(t, 0.0, testutil.ToFloat64(m.WatcherRunning("users")))

	m.SetRetryQueueDepth("users", 42)
	assert.Equal(t, 42.0, testutil.ToFloat64(m.RetryQueueDepth("users")))

	m.SetDLQEntries("users", 7)
	assert.Equal(t, 7.0, testutil.ToFloat64(m.DLQEntries("users")))
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
	assert.Contains(t, body, watcherRunningName)
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
