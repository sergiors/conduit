package metrics

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	promdto "github.com/prometheus/client_model/go"

	"conduit/internal/recover"
)

// DefaultLogInterval is the fixed interval at which the worker logs a metrics
// snapshot. It is intentionally not configurable.
const DefaultLogInterval = 30 * time.Second

// MetricsLogger periodically logs a snapshot of the worker's Prometheus
// registry through the injected *log.Logger. It reads the exact same registry
// served by /metrics (via Gather), so it introduces no duplicate counters or
// state of its own.
type MetricsLogger struct {
	metrics  *Metrics
	interval time.Duration
	logger   *log.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
}

// NewMetricsLogger creates a logger that emits a metrics snapshot every interval.
// A zero or negative interval falls back to DefaultLogInterval.
func NewMetricsLogger(m *Metrics, interval time.Duration, logger *log.Logger) *MetricsLogger {
	if interval <= 0 {
		interval = DefaultLogInterval
	}
	return &MetricsLogger{
		metrics:  m,
		interval: interval,
		logger:   logger,
	}
}

// Start launches the periodic snapshot loop. It is idempotent (a second Start is
// a no-op) and nil-safe: it does nothing when the receiver or its metrics source
// is missing.
func (l *MetricsLogger) Start(ctx context.Context) error {
	if l == nil || l.metrics == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return nil
	}

	rctx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.started = true

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		recover.Protect(l.logger, "metrics:logger", func() {
			l.loop(rctx)
		})
	}()

	return nil
}

// Stop cancels the snapshot loop and waits for it to exit. It is idempotent and
// safe to call on a logger that was never started.
func (l *MetricsLogger) Stop(ctx context.Context) error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	var cancel context.CancelFunc
	l.started = false
	cancel, l.cancel = l.cancel, nil
	l.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// loop ticks on the interval and emits a snapshot until the context is
// cancelled. It also emits an immediate first snapshot so the metrics appear in
// the logs promptly rather than only after the first interval elapses (matching
// the refresher's convention of populating promptly).
func (l *MetricsLogger) loop(ctx context.Context) {
	l.snapshot()

	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.snapshot()
		}
	}
}

// snapshot gathers the current registry and emits one log line per series.
// On a gather error it logs a single warning and returns. If there are no series
// yet, it logs nothing.
func (l *MetricsLogger) snapshot() {
	if l == nil || l.metrics == nil || l.logger == nil {
		return
	}

	families, err := l.metrics.Registry().Gather()
	if err != nil {
		l.logger.Printf("failed to gather metrics snapshot: %v", err)
		return
	}

	for _, line := range snapshotLines(families) {
		l.logger.Printf("metrics %s", line)
	}
}

// snapshotLines formats a gathered registry into one line per metric series.
// It is package-private and pure so tests can assert exact output without
// timing. Histograms are rendered with only count and sum — buckets are never
// logged. Any metric with none of counter/gauge/histogram set is skipped.
func snapshotLines(families []*promdto.MetricFamily) []string {
	var lines []string
	for _, family := range families {
		if family == nil || family.Name == nil {
			continue
		}
		for _, metric := range family.Metric {
			if metric == nil {
				continue
			}
			labels := formatLabels(metric.Label)
			prefix := *family.Name
			if labels != "" {
				prefix += "{" + labels + "}"
			}
			switch {
			case metric.GetCounter() != nil:
				lines = append(lines, fmt.Sprintf("%s count=%s", prefix,
					strconv.FormatFloat(metric.GetCounter().GetValue(), 'g', -1, 64)))
			case metric.GetGauge() != nil:
				lines = append(lines, fmt.Sprintf("%s value=%s", prefix,
					strconv.FormatFloat(metric.GetGauge().GetValue(), 'g', -1, 64)))
			case metric.GetHistogram() != nil:
				lines = append(lines, fmt.Sprintf("%s count=%s sum=%s", prefix,
					strconv.FormatUint(metric.GetHistogram().GetSampleCount(), 10),
					strconv.FormatFloat(metric.GetHistogram().GetSampleSum(), 'g', -1, 64)))
			default:
				// Defensive: skip untyped/summary and anything else.
				continue
			}
		}
	}
	return lines
}

// formatLabels renders a metric's label pairs as `k=v` joined by commas. The
// client_golang client returns them sorted by label name. The receiver is
// irrelevant (pure helper grouped with the type for cohesion).
func formatLabels(pairs []*promdto.LabelPair) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p == nil {
			continue
		}
		parts = append(parts, p.GetName()+"="+p.GetValue())
	}
	return strings.Join(parts, ",")
}
