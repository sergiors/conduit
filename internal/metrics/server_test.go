package metrics

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discardLogger = log.New(io.Discard, "", 0)

// freePort returns an allocated-and-released TCP port. There is a tiny race
// between release and reuse, acceptable for tests.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	return "127.0.0.1:" + port
}

// TestServerRoundTrip starts a metrics server on a random port, scrapes
// /metrics, and asserts the Prometheus exposition content includes a set gauge.
func TestServerRoundTrip(t *testing.T) {
	m := New()
	m.SetWatcherRunning("users", true)

	srv := NewServer(freePort(t), m.Handler(), discardLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, srv.Start(ctx))

	// Discover the bound port from the server's listener.
	lisAddr := srv.boundAddr()
	require.NotEmpty(t, lisAddr, "server should have a bound listener")

	resp, err := http.Get("http://" + lisAddr + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain; version=0.0.4"),
		"expected Prometheus exposition content type, got %q", resp.Header.Get("Content-Type"))
	assert.Contains(t, string(body), watcherRunningName)
	assert.Contains(t, string(body), `conduit_watcher_running{collection="users"} 1`)

	// Stop must be clean and idempotent.
	require.NoError(t, srv.Stop(ctx))
	require.NoError(t, srv.Stop(ctx))
}

// TestServerMethodNotAllowed starts a metrics server on a random port and
// asserts a non-GET request to /metrics is rejected with 405 Method Not Allowed
// carrying an Allow: GET header, rather than being served.
func TestServerMethodNotAllowed(t *testing.T) {
	m := New()
	srv := NewServer(freePort(t), m.Handler(), discardLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, srv.Start(ctx))

	lisAddr := srv.boundAddr()
	require.NotEmpty(t, lisAddr, "server should have a bound listener")

	req, err := http.NewRequest(http.MethodPost, "http://"+lisAddr+"/metrics", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Allow"), "GET")

	require.NoError(t, srv.Stop(ctx))
}

// TestServerStartBindError proves a port conflict surfaces synchronously from
// Start rather than in the serving goroutine.
func TestServerStartBindError(t *testing.T) {
	// Occupy a port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	m := New()
	srv := NewServer(l.Addr().String(), m.Handler(), discardLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Start(ctx)
	require.Error(t, err, "Start must fail synchronously on a bind conflict")
	assert.Contains(t, err.Error(), "address already in use")
}

// TestServerStopBeforeStart verifies Stop on a never-started server is a no-op.
func TestServerStopBeforeStart(t *testing.T) {
	m := New()
	srv := NewServer(freePort(t), m.Handler(), discardLogger)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, srv.Stop(ctx))
	require.NoError(t, srv.Stop(ctx))
}

// boundAddr returns the actual bound listen address once the server is serving.
func (s *Server) boundAddr() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}
