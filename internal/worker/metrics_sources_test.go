package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/dispatch"
	"conduit/internal/metrics"
	"conduit/internal/redis"
	"conduit/internal/retry"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeRetryStore is a minimal in-memory Store for the retry processor, exposing
// GetRetryQueueLength plus the no-op methods the interface requires.
type fakeRetryStore struct {
	mu     sync.Mutex
	depths map[string]int64
	fail   bool
}

func (f *fakeRetryStore) GetRetryQueueLength(ctx context.Context, collectionName string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return 0, errors.New("redis down")
	}
	return f.depths[collectionName], nil
}

func (f *fakeRetryStore) DequeueRetry(ctx context.Context, collectionName string, limit int64) ([]redis.RetryEvent, error) {
	return nil, nil
}
func (f *fakeRetryStore) EnqueueRetry(ctx context.Context, event redis.RetryEvent) error { return nil }
func (f *fakeRetryStore) RemoveRetryEvent(ctx context.Context, collectionName string, event redis.RetryEvent) error {
	return nil
}

// TestRetryQueueGaugeSource verifies the retry-queue-depth gauges are written
// for registered collections and left untouched on a read error.
func TestRetryQueueGaugeSource(t *testing.T) {
	t.Run("writes depths for registered collections", func(t *testing.T) {
		store := &fakeRetryStore{depths: map[string]int64{"users": 3, "orders": 7}}
		proc := retry.NewProcessor(store, nil, nil, retry.DefaultConfig(), discardLogger)
		proc.RegisterCollection("users")
		proc.RegisterCollection("orders")

		m := metrics.New()
		src := &retryQueueGaugeSource{processor: proc}
		src.RefreshMetrics(context.Background(), m)

		assert.Equal(t, 3.0, gaugeValue(t, m, "conduit_retry_queue_depth", "collection", "users"))
		assert.Equal(t, 7.0, gaugeValue(t, m, "conduit_retry_queue_depth", "collection", "orders"))
	})

	t.Run("leaves gauge untouched on read error", func(t *testing.T) {
		store := &fakeRetryStore{depths: map[string]int64{"users": 9}}
		proc := retry.NewProcessor(store, nil, nil, retry.DefaultConfig(), discardLogger)
		proc.RegisterCollection("users")

		m := metrics.New()
		m.SetRetryQueueDepth("users", 42) // a prior known good value

		// Now the store fails; the refresh must not zero the gauge.
		store.fail = true
		src := &retryQueueGaugeSource{processor: proc}
		src.RefreshMetrics(context.Background(), m)

		assert.Equal(t, 42.0, gaugeValue(t, m, "conduit_retry_queue_depth", "collection", "users"),
			"read error must not zero the gauge")
	})
}

// fakeDLQ is a minimal CountDLQEntries double (satisfies dlqCounter).
type fakeDLQ struct {
	counts map[string]int64
	fail   map[string]bool
}

func (f *fakeDLQ) CountDLQEntries(ctx context.Context, collectionName string) (int64, error) {
	if f.fail[collectionName] {
		return 0, errors.New("collection not found")
	}
	return f.counts[collectionName], nil
}

// fakeCollectionsLister is a minimal List double (satisfies collectionLister).
type fakeCollectionsLister struct {
	collections []collections.Collection
	err         error
}

func (f *fakeCollectionsLister) List(ctx context.Context) ([]collections.Collection, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.collections, nil
}

// TestDLQGaugeSource verifies the DLQ gauge source sets entries per managed
// collection and leaves a gauge untouched when CountDLQEntries errors for a
// collection (e.g. deleted between List and Count).
func TestDLQGaugeSource(t *testing.T) {
	t.Run("sets entries per managed collection", func(t *testing.T) {
		lister := &fakeCollectionsLister{
			collections: []collections.Collection{{CollectionName: "users"}, {CollectionName: "orders"}},
		}
		dlq := &fakeDLQ{counts: map[string]int64{"users": 2, "orders": 5}}

		m := metrics.New()
		src := &dlqGaugeSource{collectionsManager: dlq, collectionsLister: lister}
		src.RefreshMetrics(context.Background(), m)

		assert.Equal(t, 2.0, gaugeValue(t, m, "conduit_dlq_entries", "collection", "users"))
		assert.Equal(t, 5.0, gaugeValue(t, m, "conduit_dlq_entries", "collection", "orders"))
	})

	t.Run("leaves a gauge untouched when counting the collection errors", func(t *testing.T) {
		lister := &fakeCollectionsLister{
			collections: []collections.Collection{{CollectionName: "users"}, {CollectionName: "orders"}},
		}
		dlq := &fakeDLQ{counts: map[string]int64{"orders": 8}, fail: map[string]bool{"users": true}}

		m := metrics.New()
		m.SetDLQEntries("users", 10) // known good
		m.SetDLQEntries("orders", 3)

		src := &dlqGaugeSource{collectionsManager: dlq, collectionsLister: lister}
		src.RefreshMetrics(context.Background(), m)

		assert.Equal(t, 10.0, gaugeValue(t, m, "conduit_dlq_entries", "collection", "users"),
			"Count error must leave the gauge untouched")
		assert.Equal(t, 8.0, gaugeValue(t, m, "conduit_dlq_entries", "collection", "orders"))
	})

	t.Run("leaves gauges untouched when enumeration fails", func(t *testing.T) {
		lister := &fakeCollectionsLister{err: errors.New("mongo down")}
		dlq := &fakeDLQ{counts: map[string]int64{"users": 2}}

		m := metrics.New()
		m.SetDLQEntries("users", 10)

		src := &dlqGaugeSource{collectionsManager: dlq, collectionsLister: lister}
		src.RefreshMetrics(context.Background(), m)

		assert.Equal(t, 10.0, gaugeValue(t, m, "conduit_dlq_entries", "collection", "users"),
			"enumeration error must leave the gauge untouched")
	})
}

// TestRefresherLoopAndStop verifies the refresher loop periodically samples its
// sources and Stop cancels cleanly.
func TestRefresherLoopAndStop(t *testing.T) {
	m := metrics.New()
	r := metrics.NewRefresher(m, 20*time.Millisecond, discardLogger)

	// A source that signals a channel on each refresh.
	signalled := make(chan struct{}, 10)
	r.AddSource(&signallingSource{ch: signalled})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, r.Start(ctx))

	// The initial refresh signals immediately; wait for at least two refreshes
	// to prove the ticker loop is calling the source periodically.
	deadline := time.After(5 * time.Second)
	var refreshes int
	for refreshes < 2 {
		select {
		case <-signalled:
			refreshes++
		case <-deadline:
			t.Fatalf("refresher did not sample source enough times (got %d)", refreshes)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	require.NoError(t, r.Stop(stopCtx))

	// A second Stop is a no-op.
	require.NoError(t, r.Stop(context.Background()))
}

// signallingSource is a GaugeSource that signals a channel each time it runs.
type signallingSource struct {
	ch chan struct{}
}

func (s *signallingSource) RefreshMetrics(ctx context.Context, m *metrics.Metrics) {
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

// TestMetricsObserverIsMetrics verifies *metrics.Metrics satisfies
// dispatch.SinkDeliveryObserver directly (the compile-time assertion in
// metrics_sources.go) and forwards delivery observations to the metrics surface.
func TestMetricsObserverIsMetrics(t *testing.T) {
	m := metrics.New()
	var obs dispatch.SinkDeliveryObserver = m
	obs.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	obs.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, errors.New("boom"))

	assert.Equal(t, 1.0, gaugeValue(t, m, "conduit_sink_deliveries_total",
		"collection", "users", "sink_type", string(collections.SinkTypeHTTP), "outcome", "success"))
	assert.Equal(t, 1.0, gaugeValue(t, m, "conduit_sink_deliveries_total",
		"collection", "users", "sink_type", string(collections.SinkTypeHTTP), "outcome", "failure"))

	// Nil *Metrics is a no-op (nil-safety is covered by the metrics package).
	var nilMetrics *metrics.Metrics
	require.NotPanics(t, func() {
		nilMetrics.ObserveSinkDelivery("x", collections.SinkTypeHTTP, 0, errors.New("boom"))
	})
}

// gaugeValue reads the current value of a counter/gauge family for the given
// exact label pair set from the registry, returning 0 if the series is absent.
func gaugeValue(t *testing.T, m *metrics.Metrics, family string, labels ...string) float64 {
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
			labelsSet := map[string]string{}
			for _, l := range metric.GetLabel() {
				labelsSet[l.GetName()] = l.GetValue()
			}
			matched := true
			for k, v := range need {
				if labelsSet[k] != v {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if c := metric.GetCounter(); c != nil {
				return c.GetValue()
			}
			if g := metric.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}
	return 0.0
}
