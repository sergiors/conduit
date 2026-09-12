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
	collectionLabel          = "collection"
	eventTypeLabel           = "event_type"
	sinkTypeLabel            = "sink_type"
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
type Metrics struct {
	registry *prometheus.Registry

	eventsProcessed      *prometheus.CounterVec
	sinkDeliveries       *prometheus.CounterVec
	sinkDeliveryDuration *prometheus.HistogramVec
	watcherRunning       *prometheus.GaugeVec
	retryQueueDepth      *prometheus.GaugeVec
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
	}

	registry.MustRegister(
		m.eventsProcessed,
		m.sinkDeliveries,
		m.sinkDeliveryDuration,
		m.watcherRunning,
		m.retryQueueDepth,
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
	if m == nil || m.registry == nil {
		// A nil/zero Metrics cannot serve anything meaningful; return an empty
		// handler rather than nil so callers can always route it.
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveEventsProcessed increments the settled-events counter for a collection
// and event type. The event-type label uses the canonical stream record type.
// An empty event type is skipped defensively to avoid an empty label value.
func (m *Metrics) ObserveEventsProcessed(collection string, eventType streams.RecordType) {
	if m == nil || m.eventsProcessed == nil || eventType == "" {
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
	if m.sinkDeliveries != nil {
		m.sinkDeliveries.WithLabelValues(collection, sinkTypeStr, string(outcome)).Inc()
	}
	if m.sinkDeliveryDuration != nil {
		m.sinkDeliveryDuration.WithLabelValues(collection, sinkTypeStr).Observe(duration.Seconds())
	}
}

// SetWatcherRunning sets the watcher-running gauge for a collection to 1
// (running) or 0 (not running/absent).
func (m *Metrics) SetWatcherRunning(collection string, running bool) {
	if m == nil || m.watcherRunning == nil {
		return
	}
	if running {
		m.watcherRunning.WithLabelValues(collection).Set(1)
		return
	}
	m.watcherRunning.WithLabelValues(collection).Set(0)
}

// SetRetryQueueDepth sets the retry-queue-depth gauge for a collection.
func (m *Metrics) SetRetryQueueDepth(collection string, depth int64) {
	if m == nil || m.retryQueueDepth == nil {
		return
	}
	m.retryQueueDepth.WithLabelValues(collection).Set(float64(depth))
}

// SetDLQEntries sets the dead-letter-entries gauge for a collection.
func (m *Metrics) SetDLQEntries(collection string, count int64) {
	if m == nil || m.dlqEntries == nil {
		return
	}
	m.dlqEntries.WithLabelValues(collection).Set(float64(count))
}

// --- Test accessors -----------------------------------------------------------
//
// These exposed child collectors let tests assert exact values with
// prometheus/testutil without importing the concrete metric types. They are
// part of the package's public surface for verification.

// EventsProcessedTotal returns the events-processed counter child for a
// collection/event-type pair.
func (m *Metrics) EventsProcessedTotal(collection, eventType string) prometheus.Counter {
	if m == nil || m.eventsProcessed == nil {
		return nil
	}
	return m.eventsProcessed.WithLabelValues(collection, eventType)
}

// SinkDeliveries returns the sink-deliveries counter child for a
// collection/sink-type/outcome triple.
func (m *Metrics) SinkDeliveries(collection string, sinkType collections.Type, outcome Outcome) prometheus.Counter {
	if m == nil || m.sinkDeliveries == nil {
		return nil
	}
	return m.sinkDeliveries.WithLabelValues(collection, string(sinkType), string(outcome))
}

// SinkDeliveryDuration returns the sink-delivery-duration histogram child for a
// collection/sink-type pair.
func (m *Metrics) SinkDeliveryDuration(collection string, sinkType collections.Type) prometheus.Histogram {
	if m == nil || m.sinkDeliveryDuration == nil {
		return nil
	}
	obs := m.sinkDeliveryDuration.WithLabelValues(collection, string(sinkType))
	if h, ok := obs.(prometheus.Histogram); ok {
		return h
	}
	return nil
}

// WatcherRunning returns the watcher-running gauge child for a collection.
func (m *Metrics) WatcherRunning(collection string) prometheus.Gauge {
	if m == nil || m.watcherRunning == nil {
		return nil
	}
	return m.watcherRunning.WithLabelValues(collection)
}

// RetryQueueDepth returns the retry-queue-depth gauge child for a collection.
func (m *Metrics) RetryQueueDepth(collection string) prometheus.Gauge {
	if m == nil || m.retryQueueDepth == nil {
		return nil
	}
	return m.retryQueueDepth.WithLabelValues(collection)
}

// DLQEntries returns the dead-letter-entries gauge child for a collection.
func (m *Metrics) DLQEntries(collection string) prometheus.Gauge {
	if m == nil || m.dlqEntries == nil {
		return nil
	}
	return m.dlqEntries.WithLabelValues(collection)
}
