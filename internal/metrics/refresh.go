package metrics

import (
	"context"
	"log"
	"sync"
	"time"

	"conduit/internal/recover"
)

// DefaultRefreshInterval is how often the refresher samples gauge state.
const DefaultRefreshInterval = 15 * time.Second

// GaugeSource periodically samples one gauge family (retry queue depth, DLQ
// entries) and pushes the freshly read values into the metrics via the provided
// *Metrics. Sources are ordered; on a read error a source must skip the gauge
// update entirely and MUST NOT write zero or stale values into the gauge — the
// previously written value remains visible until a successful read.
type GaugeSource interface {
	// RefreshMetrics samples the source's gauges and writes them to m.
	RefreshMetrics(ctx context.Context, m *Metrics)
}

// Refresher periodically runs a set of GaugeSource implementations, so gauges
// that are expensive to compute live (MongoDB/Redis round trips) are refreshed
// lazily instead of on every scrape.
type Refresher struct {
	metrics  *Metrics
	interval time.Duration
	sources  []GaugeSource
	logger   *log.Logger

	mu      sync.Mutex
	srcMu   sync.RWMutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
}

// NewRefresher creates a refresher that re-samples the given sources every
// interval. A zero interval falls back to DefaultRefreshInterval.
func NewRefresher(m *Metrics, interval time.Duration, logger *log.Logger) *Refresher {
	if interval <= 0 {
		interval = DefaultRefreshInterval
	}
	return &Refresher{
		metrics:  m,
		interval: interval,
		logger:   logger,
	}
}

// AddSource registers a gauge source to be sampled each refresh. It may be
// called before Start (wiring time) and is safe to call concurrently.
func (r *Refresher) AddSource(s GaugeSource) {
	if r == nil || s == nil {
		return
	}
	r.srcMu.Lock()
	defer r.srcMu.Unlock()
	r.sources = append(r.sources, s)
}

// Start launches the periodic refresh loop. It is idempotent: a second Start is
// a no-op.
func (r *Refresher) Start(ctx context.Context) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}

	rctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.started = true

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		recover.Protect(r.logger, "metrics:refresher", func() {
			r.refreshLoop(rctx)
		})
	}()

	return nil
}

// Stop cancels the refresh loop and waits for it to exit. It is idempotent and
// safe to call on a refresher that was never started.
func (r *Refresher) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	var cancel context.CancelFunc
	r.started = false
	cancel, r.cancel = r.cancel, nil
	r.mu.Unlock()

	if cancel == nil {
		return nil
	}
	cancel()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// refreshAll runs every registered source once.
func (r *Refresher) refreshAll(ctx context.Context) {
	r.srcMu.RLock()
	sources := make([]GaugeSource, len(r.sources))
	copy(sources, r.sources)
	r.srcMu.RUnlock()

	if r.metrics == nil {
		return
	}
	for _, s := range sources {
		s.RefreshMetrics(ctx, r.metrics)
	}
}

// refreshLoop ticks on the interval and refreshes the sources until the context
// is cancelled. A panic inside a source must not kill the loop; the next tick
// retries.
func (r *Refresher) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Refresh once immediately so the gauges are populated promptly rather
	// than only after the first interval elapses.
	r.refreshAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, panicked := recover.ProtectErr(r.logger, "metrics:refresher", func() error {
				r.refreshAll(ctx)
				return nil
			}); panicked {
				r.logger.Println("Metrics refresh panicked; continuing loop")
			}
		}
	}
}
