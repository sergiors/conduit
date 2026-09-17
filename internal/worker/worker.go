// Package worker hosts the CDC worker runtime: MongoDB change-stream watchers
// and the retry processor run here until a shutdown signal stops them in
// dependency order.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"conduit/internal/collections"
	"conduit/internal/config"
	"conduit/internal/dispatch"
	_ "conduit/internal/dispatch/transports" // Register transport builders via init()
	"conduit/internal/metrics"
	"conduit/internal/mongo"
	"conduit/internal/redis"
	"conduit/internal/retry"
	"conduit/internal/watcher"
)

type Worker struct {
	cfg                config.Config
	collectionsManager *collections.Manager
	dispatcher         *dispatch.Dispatcher
	watcherManager     *watcher.Manager
	retryProcessor     *retry.Processor
	metrics            *metrics.Metrics
	metricsServer      *metrics.Server
	metricsRefresher   *metrics.Refresher
	metricsLogger      *metrics.MetricsLogger
	logger             *slog.Logger

	shutdownOnce atomic.Bool
}

// NewWorker creates the CDC worker from injected shared infrastructure and
// initializes the components the worker owns itself: the dispatcher (metrics
// + logging observers), the retry processor, the watcher manager, and the
// metrics stack (registry, opt-in Prometheus server, gauge refresher, registry
// logger).
//
// The MongoDB client, Redis client, and collections manager are shared
// dependencies created and owned by the application composition root
// (internal/runtime): they are injected here and closed by their owner after
// the worker has stopped — the worker never closes them. Start and stop the
// resulting component with Start/Shutdown.
func NewWorker(
	cfg config.Config,
	logger *slog.Logger,
	mongoClient *mongo.Client,
	redisClient *redis.Client,
	collectionsManager *collections.Manager,
) (*Worker, error) {
	// Initialize metrics. The worker's Prometheus surface is a dedicated,
	// non-global registry served on cfg.MetricsAddr. Metrics are opt-in: an
	// empty MetricsAddr disables them — metrics stay nil and every
	// instrumentation call site is a no-op (no server is started).
	var metricsInstance *metrics.Metrics
	var metricsServer *metrics.Server
	if cfg.MetricsAddr != "" {
		metricsInstance = metrics.New()
		metricsServer = metrics.NewServer(cfg.MetricsAddr, metricsInstance.Handler(), logger)
	}

	// Initialize dispatcher. It records per-sink delivery metrics through the
	// metrics instance when present, and logs per-sink delivery outcomes through
	// the worker's logger. Delivery logging is independent of metrics, so the
	// logger is always wired; the observer (metricsInstance) may be a nil
	// pointer, which is safe because ObserveSinkDelivery is nil-receiver safe.
	dispatcher := dispatch.NewDispatcher(dispatch.Config{}, metricsInstance, metricsInstance, logger)

	// Initialize retry processor. The collections.Manager owns the MongoDB DLQ
	// (config.dlq) and is passed as the DLQ dependency for exhausted retry
	// events; the retry queue itself stays Redis-backed.
	retryProcessor := retry.NewProcessor(
		redisClient,
		collectionsManager,
		dispatcher,
		retry.DefaultConfig(),
		logger,
	)

	// Initialize watcher manager
	watcherCfg := watcher.DefaultConfig()
	watcherManager := watcher.NewManager(
		mongoClient.Client,
		cfg.MongoDBDatabase,
		collectionsManager,
		redisClient,
		dispatcher,
		retryProcessor,
		watcherCfg,
		logger,
		metricsInstance,
	)

	// Wire the gauge sources (retry queue depth, DLQ entries) into a refresher
	// that periodically samples them so Prometheus does not pay for MongoDB /
	// Redis round trips on every scrape.
	var metricsRefresher *metrics.Refresher
	var metricsLogger *metrics.MetricsLogger
	if metricsInstance != nil {
		metricsRefresher = metrics.NewRefresher(metricsInstance, metrics.DefaultRefreshInterval, logger)
		metricsRefresher.AddSource(&retryQueueGaugeSource{processor: retryProcessor})
		metricsRefresher.AddSource(&dlqGaugeSource{collectionsManager: collectionsManager})

		// Periodically log a snapshot of the worker's registry. This runs only
		// when metrics are enabled (same guard as the refresher) and reads the
		// exact registry served by /metrics via Gather.
		metricsLogger = metrics.NewMetricsLogger(metricsInstance, metrics.DefaultLogInterval, logger)
	}

	return &Worker{
		cfg:                cfg,
		collectionsManager: collectionsManager,
		dispatcher:         dispatcher,
		watcherManager:     watcherManager,
		retryProcessor:     retryProcessor,
		metrics:            metricsInstance,
		metricsServer:      metricsServer,
		metricsRefresher:   metricsRefresher,
		metricsLogger:      metricsLogger,
		logger:             logger,
	}, nil
}

// Shutdown gracefully stops the worker in dependency order:
//
//  1. watcher manager (cancels the run ctx, waits for its loops and every
//     watcher, closes pub/sub) — no new events flow while bookkeeping drains;
//  2. retry processor (waits for the current processQueue pass to finish);
//  3. metrics refresher (stops gauges from sampling while tearing down);
//  4. metrics logger (stops emitting registry snapshots while tearing down);
//  5. dispatcher (closes all sinks/transports);
//  6. metrics server (last, so /metrics stays serving through the drain).
//
// The shared MongoDB and Redis clients are NOT closed here: they are injected
// dependencies owned by the application composition root, which closes them
// only after their consumers (API and worker) have fully stopped.
//
// Individual errors are collected and logged; the combined error is returned.
// Shutdown is idempotent: calling it more than once is a no-op.
func (w *Worker) Shutdown(ctx context.Context) error {
	if !w.shutdownOnce.CompareAndSwap(false, true) {
		return nil
	}

	w.logger.Info("Shutting down worker")

	var errs []error

	// Watcher manager goes first so no new events are dispatched while
	// in-flight bookkeeping completes.
	if err := w.watcherManager.Stop(ctx); err != nil {
		w.logger.Error("Failed to stop watcher manager", "component", "watcherManager", "error", err)
		errs = append(errs, err)
	}

	if err := w.retryProcessor.Stop(ctx); err != nil {
		w.logger.Error("Failed to stop retry processor", "component", "retryProcessor", "error", err)
		errs = append(errs, err)
	}

	// Stop the metrics refresher after the data plane so it is not sampling
	// while the gauge sources are torn down.
	if w.metricsRefresher != nil {
		if err := w.metricsRefresher.Stop(ctx); err != nil {
			w.logger.Error("Failed to stop metrics refresher", "component", "metricsRefresher", "error", err)
			errs = append(errs, err)
		}
	}

	// Stop the metrics logger right after the refresher so it is not emitting
	// registry snapshots while the data plane tears down.
	if w.metricsLogger != nil {
		if err := w.metricsLogger.Stop(ctx); err != nil {
			w.logger.Error("Failed to stop metrics logger", "component", "metricsLogger", "error", err)
			errs = append(errs, err)
		}
	}

	if err := w.dispatcher.Close(); err != nil {
		w.logger.Error("Failed to close dispatcher", "component", "dispatcher", "error", err)
		errs = append(errs, err)
	}

	// Stop the metrics server last so the /metrics endpoint stays serving
	// through the whole data-plane drain.
	if w.metricsServer != nil {
		if err := w.metricsServer.Stop(ctx); err != nil {
			w.logger.Error("Failed to stop metrics server", "component", "metricsServer", "error", err)
			errs = append(errs, err)
		}
	}

	w.logger.Info("Worker stopped")
	return errors.Join(errs...)
}

// Start boots the worker's runtime components: the metrics server, the gauge
// refresher, the watcher manager, and the retry processor. Start returns once
// every component is running; ongoing work keeps running until the given
// context is cancelled. If any component fails to start, the already-started
// ones are shut down (bounded by the configured shutdown timeout) before the
// error is returned.
func (w *Worker) Start(ctx context.Context) error {
	w.logger.Info("Worker starting")

	// Start the metrics server first so Prometheus can scrape from the moment
	// the worker begins operating. A bind error (port conflict) fails startup
	// fast.
	if w.metricsServer != nil {
		if err := w.metricsServer.Start(ctx); err != nil {
			return w.startFailed(ctx, err)
		}
	}

	// Start the gauge refresher so retry/DLQ gauges populate early.
	if w.metricsRefresher != nil {
		if err := w.metricsRefresher.Start(ctx); err != nil {
			return w.startFailed(ctx, err)
		}
	}

	// Start the periodic metrics logger right after the refresher so the
	// registry is already populating before it begins emitting snapshots.
	if w.metricsLogger != nil {
		if err := w.metricsLogger.Start(ctx); err != nil {
			return w.startFailed(ctx, err)
		}
	}

	// Start watcher manager
	if err := w.watcherManager.Start(ctx); err != nil {
		return w.startFailed(ctx, err)
	}

	// Start retry processor
	if err := w.retryProcessor.Start(ctx); err != nil {
		return w.startFailed(ctx, err)
	}

	w.logger.Info("Worker started", "activeWatchers", w.watcherManager.GetActiveWatchers())

	return nil
}

// startFailed shuts an interrupted startup down in the usual dependency order
// (bounded by the configured shutdown timeout), logs the failure, and wraps the
// original error so the caller can propagate it. This previously lived in the
// process-level Run error path; with the lifecycle split it belongs to Start,
// so a failed start never leaks already-started components.
func (w *Worker) startFailed(ctx context.Context, cause error) error {
	w.logger.Error("Worker failed", "error", cause)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), w.cfg.ShutdownTimeout)
	defer cancel()
	if serr := w.Shutdown(shutdownCtx); serr != nil {
		w.logger.Error("Error during shutdown after run failure", "error", serr)
	}
	return cause
}
