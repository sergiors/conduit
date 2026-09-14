// Package metrics exposes the conduit CDC worker's operational metrics via a
// Prometheus /metrics endpoint.
//
// The package owns a dedicated prometheus.Registry (never the process-global
// default registry) so instances are fully independent and can be constructed
// in tests without duplicate-registration collisions. Metrics are served through
// promhttp.HandlerFor on a dedicated http.Server bound to METRICS_ADDR. Metrics
// are opt-in: when METRICS_ADDR is unset, no metrics server is started and the
// worker's instrumentation call sites are no-ops.
//
// When metrics are enabled, the package also logs a periodic snapshot of the
// same registry (read via Gather) through the worker's logger at a fixed 30s
// interval. This gives operators visibility into the registry without a
// Prometheus scraper, while never adding duplicate counters or state.
//
// Implemented metrics (names + labels):
//
//   - conduit_watcher_running{collection} — 1 when the collection's watcher is
//     running, 0 otherwise/absent.
//   - conduit_events_processed_total{collection,event_type} — settled events
//     (delivered or durably queued for retry), by collection and event type.
//   - conduit_sink_deliveries_total{collection,sink_type,outcome} — per-sink
//     delivery attempts, with outcome bounded to success/failure.
//   - conduit_sink_delivery_duration_seconds{collection,sink_type} — histogram
//     of per-sink delivery attempts.
//   - conduit_retry_queue_depth{collection} — gauge of a collection's retry
//     queue depth.
//   - conduit_sink_queue_depth{collection,sink_type,sink_id} — gauge of the
//     current number of events waiting in a sink lane's bounded queue.
//   - conduit_sink_queue_capacity{collection,sink_type,sink_id} — configured
//     capacity of a sink lane's bounded queue.
//   - conduit_sink_enqueue_wait_duration_seconds{collection,sink_type,sink_id}
//     — histogram of the time the dispatcher waited to place an event into a
//     sink lane's bounded queue.
//   - conduit_sink_queue_full_total{collection,sink_type,sink_id} — count of
//     enqueue attempts that hit an already-full sink lane queue.
//   - conduit_dlq_entries{collection} — gauge of a collection's dead-letter
//     entry count.
//
// Every method is nil-safe so call sites can hold a nil *Metrics when metrics
// are disabled.
package metrics
