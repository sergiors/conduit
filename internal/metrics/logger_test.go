package metrics

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/streams"
)

// safeBuffer is a minimal thread-safe writer+buffer for test capture: the
// logger goroutine writes via Write while the test reads via String/Len, so a
// mutex guards both sides to keep -race clean. io.Writer is implemented by
// locking and writing to an inner bytes.Buffer.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// bufferLogger returns a *slog.Logger writing INFO-level text to a *safeBuffer
// so tests can capture and inspect emitted lines without racing the background
// goroutine. The metrics snapshot logs at INFO, so an INFO threshold captures
// every metrics line.
func bufferLogger(buf *safeBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// TestSnapshotLinesFormat populates every metric type and asserts the exact
// snapshotLines output: label ordering, count vs value vs count+sum rendering,
// and that histogram buckets are never logged.
func TestSnapshotLinesFormat(t *testing.T) {
	m := New()

	// Counter (events processed) ×2.
	m.ObserveEventsProcessed("users", streams.InsertRecord)
	m.ObserveEventsProcessed("users", streams.InsertRecord)

	// Gauge (retry queue depth) for two collections.
	m.SetRetryQueueDepth("users", 0)
	m.SetRetryQueueDepth("orders", 42)

	// Sink delivery: success histogram (count=1, sum=1.437) plus a failure
	// outcome counter on the sink-deliveries family.
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 1437*time.Millisecond, nil)
	m.ObserveSinkDelivery("users", collections.SinkTypeHTTP, 2*time.Millisecond, assert.AnError)

	families, err := m.Registry().Gather()
	require.NoError(t, err)
	lines := snapshotLines(families)

	// Events-processed counter, labels preserved in registration order.
	assert.Contains(t, lines, `conduit_events_processed_total{collection=users,event_type=INSERT} count=2`)

	// Retry queue depth gauges use value= for both collections.
	assert.Contains(t, lines, `conduit_retry_queue_depth{collection=users} value=0`)
	assert.Contains(t, lines, `conduit_retry_queue_depth{collection=orders} value=42`)

	// Sink-delivery histogram: count=... sum=... in that order, no buckets.
	assert.Contains(t, lines, `conduit_sink_delivery_duration_seconds{collection=users,sink_type=http} count=2 sum=1.439`)

	// Sink-delivery outcome counters use count=; labels are sorted by name.
	assert.Contains(t, lines, `conduit_sink_deliveries_total{collection=users,outcome=success,sink_type=http} count=1`)
	assert.Contains(t, lines, `conduit_sink_deliveries_total{collection=users,outcome=failure,sink_type=http} count=1`)

	// Histogram buckets must never be logged.
	assert.NotContains(t, strings.Join(lines, "\n"), "bucket", "no line may reference histogram buckets")
}

// TestSnapshotLinesEmptyRegistry ensures a fresh registry with no series
// produces no lines and snapshot logs nothing.
func TestSnapshotLinesEmptyRegistry(t *testing.T) {
	m := New()
	families, err := m.Registry().Gather()
	require.NoError(t, err)
	require.Empty(t, families)

	assert.Empty(t, snapshotLines(families))

	var buf safeBuffer
	l2 := NewMetricsLogger(m, DefaultLogInterval, bufferLogger(&buf))
	l2.snapshot()
	assert.Equal(t, "", buf.String(), "snapshot with no series must log nothing")
}

// TestMetricsLoggerLogsPeriodically starts a logger with a tiny interval and
// asserts it emits a `Metrics conduit_` line within a generous window, then
// stops cleanly.
func TestMetricsLoggerLogsPeriodically(t *testing.T) {
	m := New()
	m.SetWatcherRunning("users", true)

	var buf safeBuffer
	logger := bufferLogger(&buf)
	l := NewMetricsLogger(m, 10*time.Millisecond, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, l.Start(ctx))

	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), `msg=Metrics series="conduit_`)
	}, 2*time.Second, 5*time.Millisecond, "expected a periodic metrics log line")

	// Live-format check: a gauge present before Start must surface as value=.
	assert.Contains(t, buf.String(), `series="conduit_watcher_running{collection=users} value=1"`)

	require.NoError(t, l.Stop(context.Background()))
}

// TestMetricsLoggerStopClean verifies Stop returns immediately, is idempotent,
// and that no further lines are emitted after Stop.
func TestMetricsLoggerStopClean(t *testing.T) {
	m := New()
	m.SetWatcherRunning("users", true)

	var buf safeBuffer
	l := NewMetricsLogger(m, 10*time.Millisecond, bufferLogger(&buf))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, l.Start(ctx))

	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), `msg=Metrics series="conduit_`)
	}, 2*time.Second, 5*time.Millisecond, "expected at least one metrics log line")

	require.NoError(t, l.Stop(context.Background()))
	require.NoError(t, l.Stop(context.Background()), "second Stop must be a no-op")

	before := buf.Len()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, before, buf.Len(), "no new lines may be emitted after Stop")
}

// TestMetricsLoggerStopIdempotent verifies a Start-then-Stop-twice sequence is
// clean: the first Stop returns, the second is a no-op.
func TestMetricsLoggerStopIdempotent(t *testing.T) {
	m := New()
	l := NewMetricsLogger(m, DefaultLogInterval, discardLogger)
	require.NoError(t, l.Start(context.Background()))
	require.NoError(t, l.Stop(context.Background()))
	require.NoError(t, l.Stop(context.Background()))
}

// TestMetricsLoggerNeverStartedStop verifies Stop alone (without Start) works.
func TestMetricsLoggerNeverStartedStop(t *testing.T) {
	m := New()
	l := NewMetricsLogger(m, DefaultLogInterval, discardLogger)
	require.NoError(t, l.Stop(context.Background()))
}

// TestMetricsLoggerNilSafety covers a nil receiver and a logger constructed with
// nil metrics: Start/Stop are no-ops and nothing is logged.
func TestMetricsLoggerNilSafety(t *testing.T) {
	var nilLogger *MetricsLogger
	require.NoError(t, nilLogger.Start(context.Background()))
	require.NoError(t, nilLogger.Stop(context.Background()))

	var buf safeBuffer
	l := NewMetricsLogger(nil, DefaultLogInterval, bufferLogger(&buf))
	require.NoError(t, l.Start(context.Background()))
	require.NoError(t, l.Stop(context.Background()))
	l.snapshot()
	assert.Equal(t, "", buf.String(), "nil-metrics logger must produce no output")
}

// TestMetricsLoggerLoggerMissing ensures a nil logger does not crash the loop.
func TestMetricsLoggerLoggerMissing(t *testing.T) {
	m := New()
	l := NewMetricsLogger(m, DefaultLogInterval, nil)
	require.NoError(t, l.Start(context.Background()))
	require.NoError(t, l.Stop(context.Background()))
}
