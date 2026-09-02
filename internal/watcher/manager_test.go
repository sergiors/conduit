package watcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	_ "github.com/sergiors/conduit/internal/dispatch/transports" // register HTTP transport builder
	"github.com/sergiors/conduit/internal/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// newDeterministicWatcherClient returns a lazily-connected mongo client pointed
// at an unroutable address with a short server-selection timeout, so a watcher
// created by startWatcher fails fast on connect instead of hanging or panicking
// on a nil client. It lets a successful sink load start a watcher goroutine
// (which immediately trips server selection) without requiring a live MongoDB.
func newDeterministicWatcherClient(t *testing.T) *mongo.Client {
	t.Helper()
	client, err := mongo.Connect(
		context.Background(),
		options.Client().
			ApplyURI("mongodb://127.0.0.1:1").
			SetServerSelectionTimeout(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client
}

// fakeCollectionsStore is a deterministic in-memory collectionsStore for the
// startWatcher/refreshSinks sink-load tests.
type fakeCollectionsStore struct {
	collections map[string]bool
	sinks       map[string][]collections.Sink
	// getSinksErr, when non-nil, makes GetSinks fail, simulating a persisted
	// sink config-store read failure.
	getSinksErr error
}

func newFakeCollectionsStore() *fakeCollectionsStore {
	return &fakeCollectionsStore{
		collections: make(map[string]bool),
		sinks:       make(map[string][]collections.Sink),
	}
}

func (f *fakeCollectionsStore) ListStreamEnabled(ctx context.Context) ([]collections.Collection, error) {
	var out []collections.Collection
	for name, enabled := range f.collections {
		if enabled {
			out = append(out, collections.Collection{CollectionName: name})
		}
	}
	return out, nil
}

func (f *fakeCollectionsStore) Get(ctx context.Context, name string) (*collections.Collection, error) {
	enabled, ok := f.collections[name]
	if !ok {
		return nil, collections.ErrCollectionNotFound
	}
	return &collections.Collection{CollectionName: name, StreamEnabled: enabled}, nil
}

func (f *fakeCollectionsStore) GetSinks(ctx context.Context, collectionName string) ([]collections.Sink, error) {
	if f.getSinksErr != nil {
		return nil, f.getSinksErr
	}
	return f.sinks[collectionName], nil
}

func (f *fakeCollectionsStore) setSinks(collectionName string, sinks []collections.Sink) {
	f.sinks[collectionName] = sinks
}

func (f *fakeCollectionsStore) setGetSinksErr(err error) {
	f.getSinksErr = err
}

const failClosedColl = "fail_closed_coll"

// newFailClosedWatcherManager builds a manager wired for the deterministic
// sink-load tests: an unroutable mongo client, a configurable fake collections
// store, a fake redis, a real dispatcher (so runtime lane counts are
// observable), and a real retry processor (so collection registration is
// inspectable). runCtx/runCancel are initialized so startWatcher can proceed.
func newFailClosedWatcherManager(t *testing.T, store *fakeCollectionsStore) (*Manager, *fakeRedis, *dispatch.Dispatcher, *retry.Processor) {
	t.Helper()
	client := newDeterministicWatcherClient(t)
	fr := newFakeRedis()
	disp := dispatch.NewDispatcher()
	proc := retry.NewProcessor(nil, nil, disp, retry.DefaultConfig())
	mgr := NewManager(client, "conduit", store, fr, disp, proc, DefaultConfig())
	mgr.runCtx, mgr.runCancel = context.WithCancel(context.Background())
	t.Cleanup(mgr.runCancel)
	return mgr, fr, disp, proc
}

// TestStartWatcherSinkLoadFailClosed verifies the watcher starts with the
// configured sinks when the persisted sink configuration loads successfully.
func TestStartWatcherSinkLoadSucceeds(t *testing.T) {
	store := newFakeCollectionsStore()
	// Collection must exist so a (fake) enablement record is consistent with a
	// collection that is stream-enabled with configured sinks.
	store.collections[failClosedColl] = true
	store.setSinks(failClosedColl, []collections.Sink{
		{ID: "s1", Type: collections.SinkTypeHTTP, Spec: map[string]interface{}{"endpoint": "http://127.0.0.1:1/events"}},
	})

	mgr, _, disp, proc := newFailClosedWatcherManager(t, store)

	err := mgr.startWatcher(mgr.runCtx, collections.Collection{CollectionName: failClosedColl})
	require.NoError(t, err, "successful sink load must allow the watcher to start")

	assert.Len(t, mgr.watchers, 1, "watcher must be registered")
	require.NotNil(t, mgr.watchers[failClosedColl], "watcher must exist for collection")

	mgr.mu.RLock()
	stored := mgr.currentSinks[failClosedColl]
	mgr.mu.RUnlock()
	require.Len(t, stored, 1, "currentSinks must be written with the loaded sinks")

	assert.Equal(t, 1, disp.SinkCount(failClosedColl), "dispatcher must have a lane for the configured sink")
	assert.True(t, proc.IsCollectionRegistered(failClosedColl), "collection must be registered with the retry processor")
}

// TestStartWatcherSinkLoadZeroSinks verifies that a successfully-loaded empty
// ([]/nil) sink configuration is legitimate and starts the watcher normally
// with zero runtime lanes.
func TestStartWatcherSinkLoadZeroSinks(t *testing.T) {
	store := newFakeCollectionsStore()
	// No sinks configured: GetSinks returns nil (mapped to an empty result),
	// which must not be treated as a failure.
	store.collections[failClosedColl] = true

	mgr, _, disp, proc := newFailClosedWatcherManager(t, store)

	err := mgr.startWatcher(mgr.runCtx, collections.Collection{CollectionName: failClosedColl})
	require.NoError(t, err, "zero-sink configuration must allow the watcher to start")

	assert.Len(t, mgr.watchers, 1, "watcher must be registered")
	require.NotNil(t, mgr.watchers[failClosedColl], "watcher must exist for collection")

	assert.Equal(t, 0, disp.SinkCount(failClosedColl), "zero configured sinks => zero runtime lanes")
	assert.True(t, proc.IsCollectionRegistered(failClosedColl), "collection must still be registered (watcher started)")
}

// TestStartWatcherSinkLoadFailure verifies the fail-closed production blocker:
// a persisted sink load failure must abort startup with no side effects — no
// watcher registered, no currentSinks written, no dispatcher lanes, and no
// retry-processor registration. The resume token must not advance nor any
// idempotency state be written, because the watcher never starts.
func TestStartWatcherSinkLoadFailure(t *testing.T) {
	store := newFakeCollectionsStore()
	store.collections[failClosedColl] = true
	sentinel := errors.New("config store down")
	store.setGetSinksErr(sentinel)

	// Pre-seed a resume token so we can assert it is NOT advanced/cleared.
	// Seed via the map directly (not SetResumeToken) so resumeCalls stays at
	// its baseline 0 and the "no token write" assertion below is meaningful.
	mgr, fr, disp, proc := newFailClosedWatcherManager(t, store)
	fr.resumeTokens[failClosedColl] = "existing-token"

	err := mgr.startWatcher(mgr.runCtx, collections.Collection{CollectionName: failClosedColl})
	require.Error(t, err, "sink load failure must abort startup")
	assert.ErrorContains(t, err, failClosedColl, "error must carry the collection name")
	assert.ErrorContains(t, err, "config store down", "error must carry the underlying cause")

	// No side effects for the next reconciliation attempt.
	mgr.mu.Lock()
	_, watcherExists := mgr.watchers[failClosedColl]
	_, sinkExists := mgr.currentSinks[failClosedColl]
	mgr.mu.Unlock()
	assert.False(t, watcherExists, "no watcher may be registered on sink-load failure")
	assert.False(t, sinkExists, "no currentSinks entry may be written on sink-load failure")

	assert.Equal(t, 0, disp.SinkCount(failClosedColl), "dispatcher must have no lanes (empty config must not register)")
	assert.False(t, proc.IsCollectionRegistered(failClosedColl), "collection must not be registered with retry processor")

	// Resume token and idempotency state must be untouched: no SetResumeToken
	// call, no MarkProcessed call, no retry enqueue.
	assert.Equal(t, 0, fr.resumeCalls, "no resume-token write may happen (watcher never starts)")
	assert.Equal(t, 0, fr.markCalls, "no idempotency write may happen (no events processed)")
	assert.Equal(t, 0, fr.enqueueCalls, "no retry enqueue may happen")
	assert.Equal(t, "existing-token", fr.resumeToken(failClosedColl), "the stored resume token must be preserved")
}

// TestStartWatcherSinkLoadFailureRecovers verifies the subsequent reconciliation
// path: after a failed start leaves no watcher, a later successful sink load
// (e.g. the config store has recovered) allows startWatcher to succeed normally.
func TestStartWatcherSinkLoadFailureRecovers(t *testing.T) {
	store := newFakeCollectionsStore()
	store.collections[failClosedColl] = true
	store.setGetSinksErr(errors.New("config store down"))

	mgr, _, disp, proc := newFailClosedWatcherManager(t, store)

	// First attempt fails closed.
	err := mgr.startWatcher(mgr.runCtx, collections.Collection{CollectionName: failClosedColl})
	require.Error(t, err)
	assert.Empty(t, mgr.watchers, "no watcher after a failed start")

	// Recovering: the next reconciliation attempt finds the store healthy and
	// the sink configuration loads.
	store.setGetSinksErr(nil)
	store.setSinks(failClosedColl, []collections.Sink{
		{ID: "s1", Type: collections.SinkTypeHTTP, Spec: map[string]interface{}{"endpoint": "http://127.0.0.1:1/events"}},
	})

	err = mgr.startWatcher(mgr.runCtx, collections.Collection{CollectionName: failClosedColl})
	require.NoError(t, err, "a later successful sink load must allow the watcher to start")

	require.Len(t, mgr.watchers, 1, "watcher must be registered after recovery")
	assert.Equal(t, 1, disp.SinkCount(failClosedColl), "recovered watcher must register its sink lane")
	assert.True(t, proc.IsCollectionRegistered(failClosedColl), "recovered watcher must be registered with retry processor")
}

// TestRefreshSinksLoadFailureKeepsLastKnownGood verifies the already-running
// refresh path never replaces known sink state with an empty/unknown state on a
// load failure: the last-known-good currentSinks and the runtime lanes must be
// left unchanged.
func TestRefreshSinksLoadFailureKeepsLastKnownGood(t *testing.T) {
	store := newFakeCollectionsStore()
	store.collections[failClosedColl] = true
	store.setSinks(failClosedColl, []collections.Sink{
		{ID: "s1", Type: collections.SinkTypeHTTP, Spec: map[string]interface{}{"endpoint": "http://127.0.0.1:1/a"}},
		{ID: "s2", Type: collections.SinkTypeHTTP, Spec: map[string]interface{}{"endpoint": "http://127.0.0.1:1/b"}},
	})

	mgr, _, disp, proc := newFailClosedWatcherManager(t, store)

	// Start a healthy watcher with two sinks.
	require.NoError(t, mgr.startWatcher(mgr.runCtx, collections.Collection{CollectionName: failClosedColl}))
	require.Equal(t, 2, disp.SinkCount(failClosedColl), "startup registers both sinks")

	// Capture the last-known-good currentSinks.
	mgr.mu.RLock()
	lastKnownGood := mgr.currentSinks[failClosedColl]
	mgr.mu.RUnlock()
	require.Len(t, lastKnownGood, 2)

	// Now the config store fails; refreshSinks must return the error and leave
	// the last-known-good state untouched.
	store.setGetSinksErr(errors.New("config store down"))
	err := mgr.refreshSinks(context.Background(), failClosedColl)
	require.Error(t, err, "refreshSinks must propagate the load failure")
	assert.ErrorContains(t, err, "config store down")

	mgr.mu.RLock()
	after := mgr.currentSinks[failClosedColl]
	mgr.mu.RUnlock()
	assert.Len(t, after, 2, "currentSinks must retain the last-known-good sinks")
	assert.Equal(t, lastKnownGood, after, "currentSinks must be unchanged on load failure")

	assert.Equal(t, 2, disp.SinkCount(failClosedColl), "runtime lanes must be unchanged on load failure")
	assert.True(t, proc.IsCollectionRegistered(failClosedColl), "collection must remain registered with retry processor")

	// A successful refresh later reconciles normally.
	store.setGetSinksErr(nil)
	require.NoError(t, mgr.refreshSinks(context.Background(), failClosedColl))
	assert.Equal(t, 2, disp.SinkCount(failClosedColl), "recovery refresh keeps the two sinks")
}
