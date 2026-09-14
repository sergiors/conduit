// Package metrics implements the Prometheus metrics surface of the conduit CDC
// worker. It owns a dedicated, non-global prometheus registry and exposes the
// worker's operational gauges/counters so Prometheus can scrape the process.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"conduit/internal/collections"
	"conduit/internal/streams"
)

// Outcome is a bounded value for the `outcome` label of the sink-delivery
// counter. It records whether a transport's delivery attempt succeeded or
// failed. Keeping the values as typed constants (rather than free-form strings)
// bounds the label cardinality to exactly these two values.
type Outcome string

const (
	// OutcomeSuccess records a delivery attempt that returned no error.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure records a delivery attempt that returned an error.
	OutcomeFailure Outcome = "failure"
)

// Metric names, kept exact for documentation and test assertions.
const (
	sinkDeliveriesName       = "conduit_sink_deliveries_total"
	sinkDeliveryDurationName = "conduit_sink_delivery_duration_seconds"
	eventsProcessedName      = "conduit_events_processed_total"
	watcherRunningName       = "conduit_watcher_running"
	retryQueueDepthName      = "conduit_retry_queue_depth"
	dlqEntriesName           = "conduit_dlq_entries"
	sinkQueueDepthName       = "conduit_sink_queue_depth"
	sinkQueueCapacityName    = "conduit_sink_queue_capacity"
	sinkEnqueueWaitName      = "conduit_sink_enqueue_wait_duration_seconds"
	sinkQueueFullName        = "conduit_sink_queue_full_total"
	collectionLabel          = "collection"
	eventTypeLabel           = "event_type"
	sinkTypeLabel            = "sink_type"
	sinkIDLabel              = "sink_id"
	outcomeLabel             = "outcome"
)

// Metrics owns the worker's Prometheus metrics on a dedicated registry. All
// collectors are registered in New(); a fresh New() instance is fully
// independent from every other, so parallel tests never collide on duplicate
// registration (the reason we avoid the process-global default registry).
//
// Every instrumentation method is nil-safe: it is a no-op when the receiver is
// nil, so call sites can hold a nil *Metrics when metrics are disabled and
// tests need not construct metrics everywhere.
//
// Metrics must be constructed with New; the zero value is not usable.
type Metrics struct {
	registry *prometheus.Registry

	eventsProcessed      *prometheus.CounterVec
	sinkDeliveries       *prometheus.CounterVec
	sinkDeliveryDuration *prometheus.HistogramVec
	watcherRunning       *prometheus.GaugeVec
	retryQueueDepth      *prometheus.GaugeVec
	sinkQueueDepth       *prometheus.GaugeVec
	sinkQueueCapacity    *prometheus.GaugeVec
	sinkEnqueueWait      *prometheus.HistogramVec
	sinkQueueFull        *prometheus.CounterVec
	dlqEntries           *prometheus.GaugeVec
}

// New creates a Metrics instance with every metric family registered on its
// own registry. It is always independent of other instances.
func New() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		registry: registry,
		eventsProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: eventsProcessedName,
			Help: "Total number of events settled (delivered or durably queued for retry), by collection and event type.",
		}, []string{collectionLabel, eventTypeLabel}),
		sinkDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: sinkDeliveriesName,
			Help: "Total number of per-sink delivery attempts, by collection, sink type and outcome. Success includes events accepted by the sink's routing path (a sink filter may skip the transport).",
		}, []string{collectionLabel, sinkTypeLabel, outcomeLabel}),
		sinkDeliveryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    sinkDeliveryDurationName,
			Help:    "Duration of per-sink delivery attempts in seconds, by collection and sink type.",
			Buckets: prometheus.DefBuckets,
		}, []string{collectionLabel, sinkTypeLabel}),
		watcherRunning: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: watcherRunningName,
			Help: "1 if a collection's watcher is running, 0 otherwise.",
		}, []string{collectionLabel}),
		retryQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: retryQueueDepthName,
			Help: "Current number of events in a collection's retry queue.",
		}, []string{collectionLabel}),
		dlqEntries: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: dlqEntriesName,
			Help: "Current number of dead-letter entries persisted for a collection.",
		}, []string{collectionLabel}),
		sinkQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: sinkQueueDepthName,
			Help: "Current number of events waiting in a sink lane's bounded queue, by collection, sink type and sink id.",
		}, []string{collectionLabel, sinkTypeLabel, sinkIDLabel}),
		sinkQueueCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: sinkQueueCapacityName,
			Help: "Configured capacity of a sink lane's bounded queue, by collection, sink type and sink id.",
		}, []string{collectionLabel, sinkTypeLabel, sinkIDLabel}),
		// Queue waits under real backpressure can far exceed the 10s ceiling of
		// prometheus.DefBuckets (the delivery-latency convention), which would
		// collapse all meaningful backpressure observations into +Inf; an
		// exponential scale from 1ms covers both the no-contention fast path and
		// long blocked waits.
		sinkEnqueueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    sinkEnqueueWaitName,
			Help:    "Duration the dispatcher waited before an event was successfully placed into a sink lane's bounded queue, by collection, sink type and sink id.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
		}, []string{collectionLabel, sinkTypeLabel, sinkIDLabel}),
		sinkQueueFull: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: sinkQueueFullName,
			Help: "Total number of enqueue attempts that encountered an already-full sink lane queue and had to apply backpressure.",
		}, []string{collectionLabel, sinkTypeLabel, sinkIDLabel}),
	}

	registry.MustRegister(
		m.eventsProcessed,
		m.sinkDeliveries,
		m.sinkDeliveryDuration,
		m.watcherRunning,
		m.retryQueueDepth,
		m.sinkQueueDepth,
		m.sinkQueueCapacity,
		m.sinkEnqueueWait,
		m.sinkQueueFull,
		m.dlqEntries,
	)

	return m
}

// Registry returns the underlying prometheus registry. It lets tests (and the
// promhttp handler) inspect or serve the collected metrics directly.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// Handler returns an http.Handler serving the metrics in Prometheus exposition
// format via promhttp.HandlerFor (not the global registry).
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		// A nil Metrics cannot serve anything meaningful; return an empty handler
		// rather than nil so callers can always route it.
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveEventsProcessed increments the settled-events counter for a collection
// and event type. The event-type label uses the canonical stream record type.
// An empty event type is skipped defensively to avoid an empty label value.
func (m *Metrics) ObserveEventsProcessed(collection string, eventType streams.RecordType) {
	if m == nil || eventType == "" {
		return
	}
	m.eventsProcessed.WithLabelValues(collection, string(eventType)).Inc()
}

// ObserveSinkDelivery records a sink delivery attempt outcome and its duration.
// The outcome is derived from err (non-nil => failure). dispatch packages the
// collection/sink-type/duration/err into a single observation so the counter
// increment and histogram recording stay atomic from the caller's perspective.
func (m *Metrics) ObserveSinkDelivery(collection string, sinkType collections.Type, duration time.Duration, err error) {
	if m == nil {
		return
	}
	outcome := OutcomeSuccess
	if err != nil {
		outcome = OutcomeFailure
	}
	sinkTypeStr := string(sinkType)
	m.sinkDeliveries.WithLabelValues(collection, sinkTypeStr, string(outcome)).Inc()
	m.sinkDeliveryDuration.WithLabelValues(collection, sinkTypeStr).Observe(duration.Seconds())
}

// SetWatcherRunning sets the watcher-running gauge for a collection to 1
// (running) or 0 (not running/absent).
func (m *Metrics) SetWatcherRunning(collection string, running bool) {
	if m == nil {
		return
	}
	v := 0.0
	if running {
		v = 1
	}
	m.watcherRunning.WithLabelValues(collection).Set(v)
}

// SetRetryQueueDepth sets the retry-queue-depth gauge for a collection.
func (m *Metrics) SetRetryQueueDepth(collection string, depth int64) {
	if m == nil {
		return
	}
	m.retryQueueDepth.WithLabelValues(collection).Set(float64(depth))
}

// SetDLQEntries sets the dead-letter-entries gauge for a collection.
func (m *Metrics) SetDLQEntries(collection string, count int64) {
	if m == nil {
		return
	}
	m.dlqEntries.WithLabelValues(collection).Set(float64(count))
}

// ObserveSinkQueueDepth sets the sink queue depth gauge for a sink lane to the
// current number of queued events. It matches the interface method name used by
// dispatch's backpressure observation so *Metrics satisfies
// SinkBackpressureObserver.
func (m *Metrics) ObserveSinkQueueDepth(collection string, sinkType collections.Type, sinkID string, depth int) {
	if m == nil {
		return
	}
	m.sinkQueueDepth.WithLabelValues(collection, string(sinkType), sinkID).Set(float64(depth))
}

// SetSinkQueueCapacity sets the configured capacity gauge for a sink lane to
// the bounded-queue size used at lane creation. It is a gauge (not an
// accumulator): setting it again overwrites the previous value.
func (m *Metrics) SetSinkQueueCapacity(collection string, sinkType collections.Type, sinkID string, capacity int) {
	if m == nil {
		return
	}
	m.sinkQueueCapacity.WithLabelValues(collection, string(sinkType), sinkID).Set(float64(capacity))
}

// ObserveSinkEnqueueWait records how long a submit waited before an event was
// successfully placed into a sink lane's bounded queue, in seconds.
func (m *Metrics) ObserveSinkEnqueueWait(collection string, sinkType collections.Type, sinkID string, wait time.Duration) {
	if m == nil {
		return
	}
	m.sinkEnqueueWait.WithLabelValues(collection, string(sinkType), sinkID).Observe(wait.Seconds())
}

// IncSinkQueueFull increments the per-lane counter of enqueue attempts that
// encountered an already-full queue and had to apply backpressure.
func (m *Metrics) IncSinkQueueFull(collection string, sinkType collections.Type, sinkID string) {
	if m == nil {
		return
	}
	m.sinkQueueFull.WithLabelValues(collection, string(sinkType), sinkID).Inc()
}

// DeleteSinkLaneMetrics removes all per-lane metric series (queue depth, queue
// capacity, enqueue wait duration, queue full total) for a permanently removed
// sink lane so stale series do not linger.
func (m *Metrics) DeleteSinkLaneMetrics(collection string, sinkType collections.Type, sinkID string) {
	if m == nil {
		return
	}
	sinkTypeStr := string(sinkType)
	m.sinkQueueDepth.DeleteLabelValues(collection, sinkTypeStr, sinkID)
	m.sinkQueueCapacity.DeleteLabelValues(collection, sinkTypeStr, sinkID)
	m.sinkEnqueueWait.DeleteLabelValues(collection, sinkTypeStr, sinkID)
	m.sinkQueueFull.DeleteLabelValues(collection, sinkTypeStr, sinkID)
}
