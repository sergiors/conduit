package worker

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/metrics"
	"conduit/internal/redis"
	"conduit/internal/retry"
)

var discardLogger = log.New(io.Discard, "", 0)

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

		assert.Equal(t, 3.0, testutil.ToFloat64(m.RetryQueueDepth("users")))
		assert.Equal(t, 7.0, testutil.ToFloat64(m.RetryQueueDepth("orders")))
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

		assert.Equal(t, 42.0, testutil.ToFloat64(m.RetryQueueDepth("users")),
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

		assert.Equal(t, 2.0, testutil.ToFloat64(m.DLQEntries("users")))
		assert.Equal(t, 5.0, testutil.ToFloat64(m.DLQEntries("orders")))
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

		assert.Equal(t, 10.0, testutil.ToFloat64(m.DLQEntries("users")),
			"Count error must leave the gauge untouched")
		assert.Equal(t, 8.0, testutil.ToFloat64(m.DLQEntries("orders")))
	})

	t.Run("leaves gauges untouched when enumeration fails", func(t *testing.T) {
		lister := &fakeCollectionsLister{err: errors.New("mongo down")}
		dlq := &fakeDLQ{counts: map[string]int64{"users": 2}}

		m := metrics.New()
		m.SetDLQEntries("users", 10)

		src := &dlqGaugeSource{collectionsManager: dlq, collectionsLister: lister}
		src.RefreshMetrics(context.Background(), m)

		assert.Equal(t, 10.0, testutil.ToFloat64(m.DLQEntries("users")),
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

// TestMetricsObserverAdapter verifies the worker's dispatch observer adapter
// forwards to the metrics instance and tolerates a nil metrics pointer.
func TestMetricsObserverAdapter(t *testing.T) {
	m := metrics.New()
	obs := metricsObserver{metrics: m}
	obs.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, nil)
	obs.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 0, errors.New("boom"))

	c, ok := m.SinkDeliveries("users", collections.SinkTypeHTTP, metrics.OutcomeSuccess).(prometheus.Counter)
	require.True(t, ok)
	assert.Equal(t, 1.0, testutil.ToFloat64(c))
	f, ok := m.SinkDeliveries("users", collections.SinkTypeHTTP, metrics.OutcomeFailure).(prometheus.Counter)
	require.True(t, ok)
	assert.Equal(t, 1.0, testutil.ToFloat64(f))

	// Nil metrics pointer is a no-op.
	require.NotPanics(t, func() {
		(metricsObserver{metrics: nil}).ObserveSinkDelivery("x", collections.SinkTypeHTTP, 0, errors.New("boom"))
	})
}
