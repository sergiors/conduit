package transports

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRecord returns a minimal StreamRecord suitable for the HTTP transport.
// The transport only marshals the record to JSON and POSTs it, so a bare insert
// record is enough.
func newTestRecord(eventID string) streams.StreamRecord {
	return streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"_id": "doc-1"},
		EventID:    eventID,
	}
}

// newHTTPTestTransport spins up an httptest server backed by handler and returns
// an HTTP transport pointed at it, plus a cleanup that stops the server.
func newHTTPTestTransport(t *testing.T, handler http.Handler) dispatch.Transport {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	tr := NewHTTP(context.Background(), HTTPSpec{Endpoint: ts.URL})
	require.NotNil(t, tr)
	return tr
}

func TestHTTPSendSuccess(t *testing.T) {
	tr := newHTTPTestTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	err := tr.Send(context.Background(), newTestRecord("evt-success"))
	require.NoError(t, err)
}

func TestHTTPSendRedirectRejected(t *testing.T) {
	// The redirect target must never be reached.
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
	}))
	t.Cleanup(target.Close)

	// The primary endpoint responds 302 pointing at the target. The transport
	// must reject the redirect and never follow it.
	tr := newHTTPTestTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))

	err := tr.Send(context.Background(), newTestRecord("evt-redirect"))
	require.Error(t, err)
	// The error must clearly signal the redirect rejection, not a bare status.
	assert.Contains(t, err.Error(), "redirect")
	assert.Contains(t, err.Error(), "delivered only to the configured endpoint")
	// The redirect target was not called.
	assert.Equal(t, int32(0), atomic.LoadInt32(&targetHits))
}

func TestHTTPSendLargeBody(t *testing.T) {
	// The body exceeds the drain budget (4 KiB). Draining is best-effort and
	// bounded, so a body larger than the budget must still count as a successful
	// delivery.
	bigBody := strings.Repeat("x", 64*1024)
	tr := newHTTPTestTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(bigBody))
	}))

	err := tr.Send(context.Background(), newTestRecord("evt-large"))
	require.NoError(t, err)
}

func TestHTTPSendNon2xx(t *testing.T) {
	tr := newHTTPTestTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))

	err := tr.Send(context.Background(), newTestRecord("evt-500"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}
