package transports

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// taskInfoBody is the JSON Meilisearch returns when it enqueues a task.
const taskInfoBody = `{"taskUid":1,"indexUid":"movies","status":"enqueued","type":"documentAdditionOrUpdate","enqueuedAt":"2024-01-01T00:00:00Z"}`

// taskSucceededBody is the JSON Meilisearch returns for a completed task.
const taskSucceededBody = `{"uid":1,"taskUid":1,"indexUid":"movies","status":"succeeded","type":"documentAdditionOrUpdate","enqueuedAt":"2024-01-01T00:00:00Z","startedAt":"2024-01-01T00:00:00Z","finishedAt":"2024-01-01T00:00:00Z"}`

// taskFailedBody is the JSON Meilisearch returns for a failed task.
const taskFailedBody = `{"uid":1,"taskUid":1,"indexUid":"movies","status":"failed","type":"documentAdditionOrUpdate","enqueuedAt":"2024-01-01T00:00:00Z","startedAt":"2024-01-01T00:00:00Z","finishedAt":"2024-01-01T00:00:00Z","error":{"message":"document limit reached","code":"max_index_limit","type":"system_error","link":""}}`

// meiliServer records the requests it receives and responds like Meilisearch.
type meiliServer struct {
	mu         sync.Mutex
	path       string
	query      string
	method     string
	body       []byte
	seenAction bool // true once a documents (non-tasks) request has been recorded

	taskBody string // response body for the /tasks endpoint
	status   int    // status code for documents endpoint, defaults to 202
}

func newMeiliServer(t *testing.T) *meiliServer {
	return &meiliServer{taskBody: taskSucceededBody}
}

func (s *meiliServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record the first non-task request (the documents PUT/DELETE). Task
		// status polling happens after, and we only care about the write request.
		if !strings.HasPrefix(r.URL.Path, "/tasks/") {
			s.mu.Lock()
			if !s.seenAction {
				s.seenAction = true
				s.path = r.URL.Path
				s.query = r.URL.RawQuery
				s.method = r.Method
				if r.Body != nil {
					s.body, _ = io.ReadAll(r.Body)
					_ = r.Body.Close()
				}
			}
			s.mu.Unlock()
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			// Task status polling endpoint.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(s.taskBody))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/documents"):
			// Update/upsert documents.
			if s.status == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(taskInfoBody))
				return
			}
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte("bad request"))
		case r.Method == http.MethodDelete:
			// Delete a single document.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(taskInfoBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func (s *meiliServer) snapshot() (path, method string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path, s.method, s.body
}

func (s *meiliServer) snapshotQuery() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query
}

func newMeiliTransport(t *testing.T) (*MeilisearchTransport, *meiliServer) {
	srv := newMeiliServer(t)
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)

	tr := NewMeilisearch(context.Background(), MeilisearchSpec{Host: ts.URL, IndexName: "movies"})
	require.NotNil(t, tr)
	return tr.(*MeilisearchTransport), srv
}

func TestMeilisearchSendUpsert(t *testing.T) {
	newImage := map[string]interface{}{"_id": "doc-1", "title": "hello"}

	tr, srv := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   newImage,
		EventID:    "evt-1",
		DocumentID: "doc-1",
	})
	require.NoError(t, err)

	path, method, body := srv.snapshot()
	assert.Equal(t, http.MethodPut, method)
	assert.Equal(t, "/indexes/movies/documents", path)
	// The SDK sends the primary key as a query param on the PUT.
	assert.Equal(t, "primaryKey=_id", srv.snapshotQuery())

	var docs []map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &docs))
	require.Len(t, docs, 1)
	// The image is sent as-is, keyed by its MongoDB `_id`; no synthetic key is
	// injected, so the received document has exactly the original fields.
	assert.Equal(t, "doc-1", docs[0]["_id"])
	assert.Equal(t, "hello", docs[0]["title"])
	assert.Len(t, docs[0], 2)
}

func TestMeilisearchSendModifyUpsert(t *testing.T) {
	newImage := map[string]interface{}{"_id": "doc-2", "title": "updated"}
	tr, srv := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.ModifyRecord,
		NewImage:   newImage,
		EventID:    "evt-2",
		DocumentID: "doc-2",
	})
	require.NoError(t, err)

	_, method, _ := srv.snapshot()
	assert.Equal(t, http.MethodPut, method)
}

func TestMeilisearchSendUpsertSendsImageVerbatim(t *testing.T) {
	newImage := map[string]interface{}{"_id": "doc-1", "title": "hello", "genre": "scifi"}
	tr, srv := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   newImage,
		EventID:    "evt-1",
		DocumentID: "doc-1",
	})
	require.NoError(t, err)

	_, _, body := srv.snapshot()
	var docs []map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &docs))
	require.Len(t, docs, 1)
	// The received document equals the full new image: all original fields,
	// `_id` included, nothing added or removed.
	assert.Equal(t, newImage, docs[0])
}

func TestMeilisearchSendModifyNoNewImageDeletes(t *testing.T) {
	oldImage := map[string]interface{}{"id": "doc-3"}
	tr, srv := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.ModifyRecord,
		OldImage:   oldImage,
		EventID:    "evt-3",
		DocumentID: "doc-3",
	})
	require.NoError(t, err)

	path, method, _ := srv.snapshot()
	assert.Equal(t, "/indexes/movies/documents/doc-3", path)
	assert.Equal(t, http.MethodDelete, method)
}

func TestMeilisearchSendRemove(t *testing.T) {
	tests := []struct {
		name       string
		documentID string
		wantID     string
	}{
		{name: "doc-4", documentID: "doc-4", wantID: "doc-4"},
		{name: "doc-5", documentID: "doc-5", wantID: "doc-5"},
		{name: "numeric id", documentID: "42", wantID: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, srv := newMeiliTransport(t)

			err := tr.Send(context.Background(), streams.StreamRecord{
				TableName:  "movies",
				RecordType: streams.RemoveRecord,
				EventID:    "evt-remove",
				DocumentID: tt.documentID,
			})
			require.NoError(t, err)

			path, method, _ := srv.snapshot()
			assert.Equal(t, "/indexes/movies/documents/"+tt.wantID, path)
			assert.Equal(t, http.MethodDelete, method)
		})
	}
}

func TestMeilisearchSendRemoveIgnoresPayload(t *testing.T) {
	// The transport must never inspect the document payload for an ID. Even when
	// the old image carries id/_id fields, the deterministic DocumentID alone
	// determines the delete target.
	tr, srv := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.RemoveRecord,
		OldImage:   map[string]interface{}{"id": "stale-id", "_id": "other"},
		EventID:    "evt-remove-docid",
		DocumentID: "deterministic-id",
	})
	require.NoError(t, err)

	path, method, _ := srv.snapshot()
	assert.Equal(t, "/indexes/movies/documents/deterministic-id", path)
	assert.Equal(t, http.MethodDelete, method)
}

func TestMeilisearchSendRemoveNoDocumentID(t *testing.T) {
	tr, _ := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.RemoveRecord,
		OldImage:   map[string]interface{}{"id": "stale-id", "_id": "other"},
		EventID:    "evt-nodocid",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no deterministic document id available")
	assert.Contains(t, err.Error(), "evt-nodocid")
	assert.Contains(t, err.Error(), "movies")
}

func TestMeilisearchSendFailedTask(t *testing.T) {
	srv := newMeiliServer(t)
	srv.taskBody = taskFailedBody
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	tr := NewMeilisearch(context.Background(), MeilisearchSpec{Host: ts.URL, IndexName: "movies"})
	require.NotNil(t, tr)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"_id": "doc-1"},
		EventID:    "evt-fail",
		DocumentID: "doc-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
	assert.Contains(t, err.Error(), "document limit reached")
}

func TestMeilisearchSendNon2xx(t *testing.T) {
	srv := newMeiliServer(t)
	srv.status = http.StatusBadRequest
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	tr := NewMeilisearch(context.Background(), MeilisearchSpec{Host: ts.URL, IndexName: "movies"})
	require.NotNil(t, tr)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"_id": "doc-1"},
		EventID:    "evt-400",
		DocumentID: "doc-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestMeilisearchSendRequestError(t *testing.T) {
	srv := newMeiliServer(t)
	ts := httptest.NewServer(srv.handler())
	serverURL := ts.URL
	ts.Close()

	tr := NewMeilisearch(context.Background(), MeilisearchSpec{Host: serverURL, IndexName: "movies"})
	require.NotNil(t, tr)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"_id": "doc-1"},
		EventID:    "evt-conn",
		DocumentID: "doc-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update documents")
	assert.Contains(t, err.Error(), "evt-conn")
	assert.Contains(t, err.Error(), "movies")
}

// TestMeilisearchEnqueueTimeout proves the enqueue call honors a caller deadline:
// the handler never responds, so Send must return a context-deadline error from
// the enqueue path ("update documents") within the caller's bound rather than
// blocking indefinitely. The waitForTask stage is never reached.
func TestMeilisearchEnqueueTimeout(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer ts.Close()
	defer close(block)

	tr := NewMeilisearch(context.Background(), MeilisearchSpec{Host: ts.URL, IndexName: "movies"})
	require.NotNil(t, tr)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := tr.Send(ctx, streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"_id": "doc-1"},
		EventID:    "evt-timeout",
		DocumentID: "doc-1",
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	// The enqueue call inherits the caller deadline, so the failure surfaces from
	// the "update documents" path with a context deadline error.
	assert.Contains(t, err.Error(), "update documents")
	assert.Contains(t, err.Error(), "context deadline exceeded")
	// Complete well under the 5s task budget to keep the suite fast.
	assert.Less(t, elapsed, 5*time.Second)
}

func TestMeilisearchErrorContext(t *testing.T) {
	srv := newMeiliServer(t)
	srv.taskBody = taskFailedBody
	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	tr := NewMeilisearch(context.Background(), MeilisearchSpec{Host: ts.URL, IndexName: "movies"})
	require.NotNil(t, tr)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"_id": "doc-1"},
		EventID:    "evt-ctx",
		DocumentID: "doc-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "evt-ctx")
	assert.Contains(t, err.Error(), "movies")
}

func TestMeilisearchUnsupportedRecordType(t *testing.T) {
	tr, _ := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.RecordType("UNKNOWN"),
		EventID:    "evt-unknown",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported record type")
}

func TestMeilisearchNoImageNoDelete(t *testing.T) {
	tr, _ := newMeiliTransport(t)

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.ModifyRecord,
		EventID:    "evt-noimg",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no new image")
}

func TestMeilisearchBuilder(t *testing.T) {
	tests := []struct {
		name    string
		spec    map[string]interface{}
		wantNil bool
	}{
		{name: "valid spec", spec: map[string]interface{}{"host": "http://localhost:7700"}, wantNil: false},
		{name: "empty host", spec: map[string]interface{}{"host": ""}, wantNil: true},
		{name: "missing host", spec: map[string]interface{}{}, wantNil: true},
		{name: "nil spec", spec: nil, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := buildMeilisearch(context.Background(), "movies", collections.SinkTypeMeilisearch, tt.spec)
			if tt.wantNil {
				assert.Nil(t, transport)
			} else {
				assert.NotNil(t, transport)
			}
		})
	}
}

func TestMeilisearchIndexNameDefault(t *testing.T) {
	transport := buildMeilisearch(context.Background(), "movies", collections.SinkTypeMeilisearch, map[string]interface{}{"host": "http://localhost:7700"})
	require.NotNil(t, transport)
	mt, ok := transport.(*MeilisearchTransport)
	require.True(t, ok)
	assert.Equal(t, "movies", mt.IndexName)
}

func TestMeilisearchClose(t *testing.T) {
	tr, _ := newMeiliTransport(t)
	assert.NoError(t, tr.Close())
}
