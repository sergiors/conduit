package watcher

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/dispatch"
	"conduit/internal/metrics"
	"conduit/internal/retry"
	"conduit/internal/streams"
)

// TestHandleEventIncrementsProcessedMetric verifies the settled-events counter
// is incremented for both settled paths: dispatch success and dispatch failure
// that settles via the retry queue. The unsettled path must NOT increment it.
func TestHandleEventIncrementsProcessedMetric(t *testing.T) {
	record := streams.StreamRecord{TableName: "users", EventID: "users:abc", RecordType: streams.InsertRecord}

	t.Run("dispatch succeeds increments", func(t *testing.T) {
		fr := newFakeRedis()
		m := metrics.New()
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{}, nil, DefaultConfig(), discardLogger, m)

		err := manager.handleEvent(context.Background(), "users", record)
		require.NoError(t, err)

		assert.Equal(t, 1.0, gaugeValue(t, m, "conduit_events_processed_total", "collection", "users", "event_type", string(streams.InsertRecord)))
	})

	t.Run("dispatch fails but retry enqueue succeeds increments", func(t *testing.T) {
		fr := newFakeRedis()
		m := metrics.New()
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{dispatchErr: errors.New("sink down")},
			nil, DefaultConfig(), discardLogger, m)

		err := manager.handleEvent(context.Background(), "users", record)
		require.NoError(t, err, "settled via the retry queue")

		assert.Equal(t, 1.0, gaugeValue(t, m, "conduit_events_processed_total", "collection", "users", "event_type", string(streams.InsertRecord)))
	})

	t.Run("dispatch fails and retry enqueue fails does not increment", func(t *testing.T) {
		fr := newFakeRedis()
		fr.enqueueResult = errors.New("redis down")
		m := metrics.New()
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{dispatchErr: errors.New("sink down")},
			nil, DefaultConfig(), discardLogger, m)

		err := manager.handleEvent(context.Background(), "users", record)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEventUnsettled)

		assert.Equal(t, 0.0, gaugeValue(t, m, "conduit_events_processed_total", "collection", "users", "event_type", string(streams.InsertRecord)))
	})

	t.Run("uses canonical event type label", func(t *testing.T) {
		fr := newFakeRedis()
		m := metrics.New()
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{}, nil, DefaultConfig(), discardLogger, m)

		rec := streams.StreamRecord{TableName: "users", EventID: "users:def", RecordType: streams.RemoveRecord}
		require.NoError(t, manager.handleEvent(context.Background(), "users", rec))

		assert.Equal(t, 1.0, gaugeValue(t, m, "conduit_events_processed_total", "collection", "users", "event_type", string(streams.RemoveRecord)))
		assert.Equal(t, 0.0, gaugeValue(t, m, "conduit_events_processed_total", "collection", "users", "event_type", string(streams.InsertRecord)))
	})
}

// TestWatcherRunningGaugeSetByStartStop verifies startWatcher sets the
// watcher-running gauge to 1 after a successful start, and stopWatcher sets it
// back to 0.
func TestWatcherRunningGaugeSetByStartStop(t *testing.T) {
	store := newFakeCollectionsStore()
	store.collections[failClosedColl] = true

	m := metrics.New()
	client := newDeterministicWatcherClient(t)
	fr := newFakeRedis()
	disp := dispatch.NewDispatcher(dispatch.Config{}, nil, nil)
	proc := retry.NewProcessor(nil, nil, disp, retry.DefaultConfig(), discardLogger)

	mgr := NewManager(client, "conduit", store, fr, disp, proc, DefaultConfig(), discardLogger, m)
	mgr.runCtx, mgr.runCancel = context.WithCancel(context.Background())
	t.Cleanup(mgr.runCancel)

	// The gauge starts absent (0 when read via testutil.ToFloat64).
	assert.Equal(t, 0.0, gaugeValue(t, m, "conduit_watcher_running", "collection", failClosedColl))

	cfg := collections.Collection{CollectionName: failClosedColl}
	err := mgr.startWatcher(mgr.runCtx, cfg)
	require.NoError(t, err, "watcher must start")
	assert.Equal(t, 1.0, gaugeValue(t, m, "conduit_watcher_running", "collection", failClosedColl),
		"startWatcher must set watcher_running to 1")

	err = mgr.stopWatcher(context.Background(), failClosedColl)
	require.NoError(t, err)
	assert.Equal(t, 0.0, gaugeValue(t, m, "conduit_watcher_running", "collection", failClosedColl),
		"stopWatcher must set watcher_running to 0")
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
