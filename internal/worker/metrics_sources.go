package worker

import (
	"context"
	"time"

	"conduit/internal/collections"
	"conduit/internal/dispatch"
	"conduit/internal/metrics"
	"conduit/internal/retry"
)

// metricsObserver adapts *metrics.Metrics to dispatch.SinkDeliveryObserver so
// the dispatcher records per-sink delivery metrics without dispatch depending
// on the metrics package. A nil metrics pointer is tolerated (no-op).
type metricsObserver struct {
	metrics *metrics.Metrics
}

func (o metricsObserver) ObserveSinkDelivery(collection string, sinkType collections.Type, duration time.Duration, err error) {
	if o.metrics == nil {
		return
	}
	o.metrics.ObserveSinkDelivery(collection, sinkType, duration, err)
}

// retryQueueGaugeSource samples the retry-queue-depth gauge for every
// collection registered with the retry processor. A read error leaves the gauge
// untouched (never zeroed) so a transient Redis blip does not misreport the
// queue as empty.
type retryQueueGaugeSource struct {
	processor *retry.Processor
}

func (s *retryQueueGaugeSource) RefreshMetrics(ctx context.Context, m *metrics.Metrics) {
	if s.processor == nil || m == nil {
		return
	}
	for _, coll := range s.processor.RegisteredCollections() {
		depth, err := s.processor.GetRetryQueueLength(ctx, coll)
		if err != nil {
			// Leave the gauge untouched on a read error.
			continue
		}
		m.SetRetryQueueDepth(coll, depth)
	}
}

// dlqCounter is the narrow interface the DLQ gauge source needs;
// *collections.Manager satisfies it.
type dlqCounter interface {
	CountDLQEntries(ctx context.Context, collectionName string) (int64, error)
}

// collectionLister lists all managed collections; *collections.Manager
// satisfies it.
type collectionLister interface {
	List(ctx context.Context) ([]collections.Collection, error)
}

// dlqGaugeSource samples the dead-letter-entries gauge for every managed
// collection. CountDLQEntries errors when the collection no longer exists
// (a List→Count race with deletion); such an error leaves the gauge untouched
// rather than zeroing it.
type dlqGaugeSource struct {
	collectionsManager dlqCounter
	collectionsLister  collectionLister
}

func (s *dlqGaugeSource) RefreshMetrics(ctx context.Context, m *metrics.Metrics) {
	if s == nil || m == nil || s.collectionsManager == nil || s.collectionsLister == nil {
		return
	}
	collections, err := s.collectionsLister.List(ctx)
	if err != nil {
		// Leave every gauge untouched when the source cannot be enumerated.
		return
	}
	for _, coll := range collections {
		count, err := s.collectionsManager.CountDLQEntries(ctx, coll.CollectionName)
		if err != nil {
			// The collection may have been deleted between List and Count;
			// leave its gauge untouched.
			continue
		}
		m.SetDLQEntries(coll.CollectionName, count)
	}
}

// Compile-time interface assertions.
var (
	_ dispatch.SinkDeliveryObserver = metricsObserver{}
	_ metrics.GaugeSource           = (*retryQueueGaugeSource)(nil)
	_ metrics.GaugeSource           = (*dlqGaugeSource)(nil)
)
