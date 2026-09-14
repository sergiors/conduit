// Package worker hosts the CDC worker runtime: MongoDB change-stream watchers
// and the retry processor run here until a shutdown signal stops them in
// dependency order.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

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
	mongoClient        *mongo.Client
	redisClient        *redis.Client
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

func NewWorker(cfg config.Config, logger *slog.Logger) (*Worker, error) {
	// Use a generous timeout for startup: MongoDB may still be electing a PRIMARY
	// after a restart, and NewClient waits for it before returning.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize MongoDB client. NewClient waits until MongoDB is ready (a
	// writable PRIMARY is reachable) before returning, so this returns once
	// MongoDB is fully ready to serve change streams.
	mongoClient, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, logger)
	if err != nil {
		return nil, err
	}

	// Initialize Redis client with URI/DSN
	redisClient, err := redis.NewClient(ctx, redis.Config{
		URI:    cfg.RedisURI,
		Prefix: "cdc:",
	}, logger)
	if err != nil {
		mongoClient.Close(ctx)
		return nil, err
	}

	// Initialize collection manager
	collectionsManager := collections.NewManager(mongoClient.Client, cfg.MongoDBDatabase, logger)

	// Create the manager's indexes (including the config.dlq dedupKey unique
	// index) so DLQ idempotency holds even when the API hasn't run yet.
	if err := collectionsManager.CreateIndex(ctx); err != nil {
		redisClient.Close()
		mongoClient.Close(ctx)
		return nil, err
	}

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
		mongoClient:        mongoClient,
		redisClient:        redisClient,
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
//  6. redis client;
//  7. mongo client;
//  8. metrics server (last, so /metrics stays serving through the drain).
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

	if err := w.redisClient.Close(); err != nil {
		w.logger.Error("Failed to close Redis", "component", "redis", "error", err)
		errs = append(errs, err)
	}

	if err := w.mongoClient.Close(ctx); err != nil {
		w.logger.Error("Failed to close MongoDB", "component", "mongo", "error", err)
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

// start boots the worker's runtime components: the metrics server, the gauge
// refresher, the watcher manager, and the retry processor.
func (w *Worker) start(ctx context.Context) error {
	w.logger.Info("Worker starting")

	// Start the metrics server first so Prometheus can scrape from the moment
	// the worker begins operating. A bind error (port conflict) fails startup
	// fast.
	if w.metricsServer != nil {
		if err := w.metricsServer.Start(ctx); err != nil {
			return err
		}
	}

	// Start the gauge refresher so retry/DLQ gauges populate early.
	if w.metricsRefresher != nil {
		if err := w.metricsRefresher.Start(ctx); err != nil {
			return err
		}
	}

	// Start the periodic metrics logger right after the refresher so the
	// registry is already populating before it begins emitting snapshots.
	if w.metricsLogger != nil {
		if err := w.metricsLogger.Start(ctx); err != nil {
			return err
		}
	}

	// Start watcher manager
	if err := w.watcherManager.Start(ctx); err != nil {
		return err
	}

	// Start retry processor
	if err := w.retryProcessor.Start(ctx); err != nil {
		return err
	}

	w.logger.Info("Worker started", "activeWatchers", w.watcherManager.GetActiveWatchers())

	return nil
}

// Run is the worker's process-level entrypoint: create the worker, run it
// until SIGINT/SIGTERM, and perform a graceful shutdown bounded by the
// configured shutdown timeout. It returns an error (which the caller should log
// and turn into a non-zero exit) rather than crashing the process.
func Run(cfg config.Config, logger *slog.Logger) error {
	worker, err := NewWorker(cfg, logger)
	if err != nil {
		return err
	}

	// SIGINT and SIGTERM both trigger a graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := worker.start(ctx); err != nil {
		logger.Error("Worker failed", "error", err)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if serr := worker.Shutdown(shutdownCtx); serr != nil {
			logger.Error("Error during shutdown after run failure", "error", serr)
		}
		return err
	}

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", "error", err)
		return err
	}

	return nil
}
