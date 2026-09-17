//go:build integration

package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"conduit/internal/config"
	"conduit/internal/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Local infra URIs for integration tests running on the host (same convention
// as internal/cli's apikey integration tests: the compose MongoDB advertises an
// internal hostname, so directConnection=true is required).
const (
	localMongoURI    = "mongodb://localhost:27017/?directConnection=true"
	localRedisURI    = "redis://localhost:6379"
	lifecycleTimeout = 60 * time.Second
)

// discardLogger is the silent logger used by all integration tests here.
var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// discardLoggerV2 returns the shared discard logger (named distinctly because
// the helper below needs a fresh logger argument).
func discardLoggerV2() *slog.Logger { return discardLogger }

// freePort reserves and immediately releases an ephemeral TCP port. There is a
// race window before another process can bind it, but for tests it is reliable
// enough: the port is picked fresh per test and only used once.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	port := addr[strings.LastIndex(addr, ":")+1:]
	require.NoError(t, ln.Close())
	return ":" + port
}

// runtimeEnv wires a unique per-test database, a free HTTP port, and (if
// metrics are enabled) the given metrics address; returns the ready-to-run
// Config.
func runtimeEnv(t *testing.T, metricsAddr string) config.Config {
	t.Helper()

	cfg := config.Config{
		MongoDBURI:      localMongoURI,
		MongoDBDatabase: fmt.Sprintf("conduit_runtime_test_%d", time.Now().UnixNano()),
		RedisURI:        localRedisURI,
		Port:            strings.TrimPrefix(freePort(t), ":"),
		ShutdownTimeout: 30 * time.Second,
		MetricsAddr:     metricsAddr,
	}
	t.Cleanup(func() {
		// Drop the per-test database off mongo's own client lifetime — a quick
		// direct connection so cleanup does not depend on the runtime's state.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = dropDatabase(ctx, localMongoURI, cfg.MongoDBDatabase)
	})
	return cfg
}

// rootContext builds the same signal-aware root context the process uses, but
// never forked off the test process — instead cancellation is manual, allowing
// the tests to drive the shutdown semantics explicitly.
func runtimeRootCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// occupiedPortAddr binds a listener and keeps it open (cleaned up by t.Cleanup)
// so the returned address can never be bound again.
func occupiedPort(t *testing.T) string {
	t.Helper()
	// Bind the wildcard, matching the metrics/API listeners ("host:port" with
	// empty host): on macOS a wildcard bind is still allowed when only a
	// 127.0.0.1 listener owns the port, so the occupied listener must itself be
	// a wildcard bind to make the conflict deterministic.
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	addr := ln.Addr().String()
	return ":" + addr[strings.LastIndex(addr, ":")+1:]
}

// waitHealthy polls the API's /health endpoint until the API is serving. The
// runtime blocks, so the test polls while Run holds the process up.
func waitHealthy(t *testing.T, port string, healthy chan<- struct{}) {
	t.Helper()
	url := "http://localhost:" + port + "/health"
	deadline := time.Now().Add(90 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			healthy <- struct{}{}
			return
		}
		if err == nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Errorf("API did not become healthy within deadline at %s", url)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestRunFullLifecycle proves that the composition-root startup works against
// live infrastructure — the API serves the shared-health endpoint, and after
// the process root context is cancelled the runtime shuts both components down
// and returns nil (Run blocks until cancellation by design).
func TestRunFullLifecycle(t *testing.T) {
	cfg := runtimeEnv(t, "")
	runCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	t.Cleanup(cancel)

	go cancelAfterHealthy(t, cfg.Port, cancel)

	err := Run(runCtx, cfg, discardLogger)
	require.NoError(t, err, "graceful shutdown after cancellation must return nil")
}

func cancelAfterHealthy(t *testing.T, port string, cancel context.CancelFunc) {
	healthy := make(chan struct{}, 1)
	go waitHealthy(t, port, healthy)
	select {
	case <-healthy:
		// Give the worker a beat to finish starting, mirroring a real SIGTERM.
		time.Sleep(300 * time.Millisecond)
		cancel()
	case <-time.After(2 * time.Minute):
		t.Error("API never became healthy")
		cancel()
	}
}

// TestRunFailedWorkerShutsDownAPI proves the failure-contract: when the worker
// fails to start (a metrics-server port conflict here), the runtime propagates
// the worker error and still shuts the API down cleanly instead of leaking it.
func TestRunFailedWorkerShutsDownAPI(t *testing.T) {
	conflict := occupiedPort(t)
	cfg := runtimeEnv(t, conflict)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, cfg, discardLogger) }()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worker")
		assert.Contains(t, err.Error(), "listen")
	case <-time.After(lifecycleTimeout):
		t.Fatal("Run did not return promptly after a worker start failure")
	}
}

// TestRunFailedAPIShutsDownWorker proves the symmetric case: if the API cannot
// bind (PORT in use), the runtime propagates the API error and shuts the worker
// down gracefully instead of leaving it running.
func TestRunFailedAPIShutsDownWorker(t *testing.T) {
	cfg := runtimeEnv(t, "")
	cfg.Port = strings.TrimPrefix(occupiedPort(t), ":")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, cfg, discardLogger) }()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "server failed")
	case <-time.After(lifecycleTimeout):
		t.Fatal("Run did not return promptly after an API bind failure")
	}
}

// dropDatabase connects directly to MongoDB and drops the given database, so
// test state never outlives the test.
func dropDatabase(ctx context.Context, uri, database string) error {
	client, err := mongo.NewClient(ctx, mongo.Config{URI: uri, Database: database}, discardLoggerV2())
	if err != nil {
		return err
	}
	defer client.Close(context.Background())
	return client.Client.Database(database).Drop(context.Background())
}
