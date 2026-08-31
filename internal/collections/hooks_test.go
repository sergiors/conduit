package collections

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookRecorder records the collection names passed to an injected hook. It is
// safe for concurrent use (OnPurge runs on a detached context, though in
// practice synchronously) and can be configured to return a fixed error so
// tests can prove a hook failure never fails the mutator.
type hookRecorder struct {
	mu    sync.Mutex
	calls []string
	err   error
}

// record returns a hook method value that appends the collection name and
// returns the recorder's configured error (nil by default).
func (r *hookRecorder) record() func(context.Context, string) error {
	return func(_ context.Context, name string) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, name)
		return r.err
	}
}

func (r *hookRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *hookRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *hookRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
}

// TestManagerDeleteHooks verifies that Delete owns the side-effect fan-out:
// OnPurge fires exactly once (before OnPublish) with the deleted collection's
// name, and OnPublish fires exactly once. Both hooks return an error to prove
// a hook failure never turns a committed deletion into an error (the (c)
// contract).
func TestManagerDeleteHooks(t *testing.T) {
	manager, _, ctx := newTestManager(t)
	const name = "hooks_delete_table"

	// Cleanup leftovers from a previous run.
	if _, err := manager.Get(ctx, name); err == nil {
		_ = manager.DisableDeletionProtection(ctx, name)
		_ = manager.Delete(ctx, name)
	}

	purgeRec := &hookRecorder{err: assert.AnError}
	publishRec := &hookRecorder{err: assert.AnError}
	manager.OnPurge = purgeRec.record()
	manager.OnPublish = publishRec.record()

	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: name}))
	require.NoError(t, manager.DisableDeletionProtection(ctx, name))

	// Reset so we only count Delete's fan-out, not the setup mutations.
	purgeRec.reset()
	publishRec.reset()

	require.NoError(t, manager.Delete(ctx, name))

	assert.Equal(t, []string{name}, purgeRec.names(), "OnPurge should fire exactly once with the name")
	assert.Equal(t, []string{name}, publishRec.names(), "OnPublish should fire exactly once with the name")
}

// TestManagerMutatorsFireOnPublish verifies that every successful configuration
// mutation fires OnPublish exactly once with the affected collection's name.
// The injected hook returns an error, so each successful call also proves a
// hook failure never fails the mutator (the (c) contract).
func TestManagerMutatorsFireOnPublish(t *testing.T) {
	manager, _, ctx := newTestManager(t)
	const name = "hooks_mutators_table"

	// Cleanup leftovers from a previous run.
	if _, err := manager.Get(ctx, name); err == nil {
		_ = manager.DisableDeletionProtection(ctx, name)
		_ = manager.Delete(ctx, name)
	}

	// Create the collection with the stream enabled so sinks can be created.
	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: name, StreamEnabled: true}))

	rec := &hookRecorder{err: assert.AnError}
	manager.OnPublish = rec.record()

	// A sink to delete in the DeleteSink case.
	sink, err := manager.CreateSink(ctx, name, Sink{
		Type: SinkTypeHTTP,
		Spec: map[string]interface{}{"endpoint": "http://localhost:3000/events"},
	})
	require.NoError(t, err)

	// resetBaseline brings the collection to a known state (stream and TTL
	// disabled) so each case's setup can rely on the pre-condition holding.
	// Both operations are idempotent, so this is safe regardless of the
	// current state.
	resetBaseline := func() {
		require.NoError(t, manager.DisableStream(ctx, name))
		require.NoError(t, manager.DisableTTL(ctx, name))
	}

	cases := []struct {
		name  string
		setup func() error
		call  func() error
	}{
		{
			name:  "EnableStream",
			setup: func() error { return nil },
			call:  func() error { return manager.EnableStream(ctx, name, false) },
		},
		{
			name:  "DisableStream",
			setup: func() error { return manager.EnableStream(ctx, name, false) },
			call:  func() error { return manager.DisableStream(ctx, name) },
		},
		{
			name:  "SetTTL",
			setup: func() error { return nil },
			call:  func() error { return manager.SetTTL(ctx, name, "expires_at") },
		},
		{
			name:  "DisableTTL",
			setup: func() error { return manager.SetTTL(ctx, name, "expires_at") },
			call:  func() error { return manager.DisableTTL(ctx, name) },
		},
		{
			name:  "CreateSink",
			setup: func() error { return manager.EnableStream(ctx, name, false) },
			call: func() error {
				_, err := manager.CreateSink(ctx, name, Sink{
					Type: SinkTypeHTTP,
					Spec: map[string]interface{}{"endpoint": "http://localhost:3000/events"},
				})
				return err
			},
		},
		{
			name:  "DeleteSink",
			setup: func() error { return nil },
			call:  func() error { return manager.DeleteSink(ctx, name, sink.ID) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetBaseline()
			require.NoError(t, tc.setup())
			rec.reset()
			require.NoError(t, tc.call())
			assert.Equal(t, 1, rec.count(), "OnPublish should fire exactly once")
			assert.Equal(t, []string{name}, rec.names())
		})
	}

	// Cleanup.
	require.NoError(t, manager.DisableDeletionProtection(ctx, name))
	require.NoError(t, manager.Delete(ctx, name))
}

// TestManagerMutatorsDoNotFireOnPublishOnFailure verifies that a failed
// mutation does not fire OnPublish. EnableStream is immutable: the second call
// returns ErrStreamAlreadyExists and must not publish.
func TestManagerMutatorsDoNotFireOnPublishOnFailure(t *testing.T) {
	manager, _, ctx := newTestManager(t)
	const name = "hooks_mutators_fail"

	// Cleanup leftovers from a previous run.
	if _, err := manager.Get(ctx, name); err == nil {
		_ = manager.DisableDeletionProtection(ctx, name)
		_ = manager.Delete(ctx, name)
	}
	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: name, StreamEnabled: false}))

	rec := &hookRecorder{}
	manager.OnPublish = rec.record()

	// First enable succeeds and fires once.
	require.NoError(t, manager.EnableStream(ctx, name, false))
	assert.Equal(t, 1, rec.count())

	// Second enable fails (immutable) and must NOT fire.
	rec.reset()
	err := manager.EnableStream(ctx, name, false)
	require.ErrorIs(t, err, ErrStreamAlreadyExists)
	assert.Equal(t, 0, rec.count(), "failed mutation must not fire OnPublish")

	// Cleanup.
	require.NoError(t, manager.DisableDeletionProtection(ctx, name))
	require.NoError(t, manager.Delete(ctx, name))
}
