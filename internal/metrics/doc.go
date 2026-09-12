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
//   - conduit_dlq_entries{collection} — gauge of a collection's dead-letter
//     entry count.
//
// Every method is nil-safe so call sites can hold a nil *Metrics when metrics
// are disabled.
package metrics
