package retry

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
)

func TestDefaultConfig(t *testing.T) {
	t.Run("returns sensible defaults", func(t *testing.T) {
		cfg := DefaultConfig()

		assert.Equal(t, 5*time.Second, cfg.Interval)
		assert.Equal(t, 5, cfg.MaxRetries)
		assert.Equal(t, 1*time.Second, cfg.BaseDelay)
		assert.Equal(t, 5*time.Minute, cfg.MaxDelay)
	})
}

func TestProcessorCreation(t *testing.T) {
	t.Run("new processor with correct configuration", func(t *testing.T) {
		cfg := DefaultConfig()
		processor := NewProcessor(nil, nil, cfg)

		assert.NotNil(t, processor)
		assert.Equal(t, cfg.Interval, processor.interval)
		assert.Equal(t, cfg.MaxRetries, processor.maxRetries)
	})
}

func TestCalculateNextRetry(t *testing.T) {
	t.Run("exponential backoff calculation", func(t *testing.T) {
		processor := &Processor{
			baseDelay: 1 * time.Second,
			maxDelay:  5 * time.Minute,
		}

		// First retry: 1s * 2^0 = 1s
		next := processor.calculateNextRetry(1)
		assert.InDelta(t, time.Now().Add(1*time.Second).Unix(), next.Unix(), 1)

		// Second retry: 1s * 2^1 = 2s
		next = processor.calculateNextRetry(2)
		assert.InDelta(t, time.Now().Add(2*time.Second).Unix(), next.Unix(), 1)

		// Third retry: 1s * 2^2 = 4s
		next = processor.calculateNextRetry(3)
		assert.InDelta(t, time.Now().Add(4*time.Second).Unix(), next.Unix(), 1)

		// Fourth retry: 1s * 2^3 = 8s
		next = processor.calculateNextRetry(4)
		assert.InDelta(t, time.Now().Add(8*time.Second).Unix(), next.Unix(), 1)
	})

	t.Run("max delay cap", func(t *testing.T) {
		processor := &Processor{
			baseDelay: 1 * time.Second,
			maxDelay:  5 * time.Minute,
		}

		// Retry 20: would be 1s * 2^19 = ~524288s, but capped at 5m
		next := processor.calculateNextRetry(20)
		assert.InDelta(t, time.Now().Add(5*time.Minute).Unix(), next.Unix(), 1)
	})
}

func TestProcessRetryEvent(t *testing.T) {
	t.Run("successful dispatch skips retry", func(t *testing.T) {
		dispatcher := dispatch.NewDispatcher()
		// Register a mock transport that always succeeds wrapped in a runtime sink.
		dispatcher.Register("users", dispatch.NewRuntimeSink(collections.Sink{}, &successTransport{}))

		processor := NewProcessor(nil, dispatcher, DefaultConfig())

		ctx := context.Background()
		eventData, _ := bson.MarshalExtJSON(streams.StreamRecord{
			TableName:  "users",
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"_id": "123"},
		}, false, false)

		event := redis.RetryEvent{
			ID:             "users-123",
			CollectionName: "users",
			EventData:      eventData,
			RetryCount:     0,
			MaxRetries:     5,
		}

		// Should succeed without panic
		processor.processRetryEvent(ctx, "users", event)
	})

	t.Run("max retries exceeded skips DLQ when redis is nil", func(t *testing.T) {
		dispatcher := dispatch.NewDispatcher()
		processor := NewProcessor(nil, dispatcher, DefaultConfig())

		ctx := context.Background()
		eventData, _ := bson.MarshalExtJSON(streams.StreamRecord{
			TableName:  "users",
			RecordType: streams.InsertRecord,
		}, false, false)

		event := redis.RetryEvent{
			ID:             "users-456",
			CollectionName: "users",
			EventData:      eventData,
			RetryCount:     5, // Already at max
			MaxRetries:     5,
		}

		// Should not panic with nil redis client
		// The SendToDLQ will fail silently (logged) but not panic
		processor.processRetryEvent(ctx, "users", event)
	})
}

func TestRetryEventStructure(t *testing.T) {
	t.Run("retry event has all required fields", func(t *testing.T) {
		event := redis.RetryEvent{
			ID:             "orders-789",
			CollectionName: "orders",
			EventData:      []byte(`{}`),
			RetryCount:     2,
			MaxRetries:     5,
			NextRetryAt:    time.Now(),
		}

		assert.Equal(t, "orders", event.CollectionName)
		assert.Equal(t, "orders-789", event.ID)
		assert.Equal(t, 2, event.RetryCount)
		assert.Equal(t, 5, event.MaxRetries)
		assert.True(t, !event.NextRetryAt.IsZero())
	})
}

func TestProcessorStop(t *testing.T) {
	t.Run("Stop before Start is safe", func(t *testing.T) {
		processor := NewProcessor(nil, nil, DefaultConfig())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		assert.NoError(t, processor.Stop(ctx))
	})

	t.Run("Start then Stop exits the loop", func(t *testing.T) {
		// Use a short interval so the loop is actively ticking; Stop must
		// cancel the loop and wait for it to exit.
		cfg := DefaultConfig()
		cfg.Interval = 10 * time.Millisecond
		processor := NewProcessor(nil, nil, cfg)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		assert.NoError(t, processor.Start(ctx))
		assert.NoError(t, processor.Stop(ctx))
	})

	t.Run("Stop is idempotent", func(t *testing.T) {
		processor := NewProcessor(nil, nil, DefaultConfig())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		assert.NoError(t, processor.Start(ctx))
		assert.NoError(t, processor.Stop(ctx))
		// Second Stop is a no-op.
		assert.NoError(t, processor.Stop(ctx))
	})

	t.Run("Start is idempotent", func(t *testing.T) {
		processor := NewProcessor(nil, nil, DefaultConfig())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		assert.NoError(t, processor.Start(ctx))
		// Second Start is a no-op.
		assert.NoError(t, processor.Start(ctx))
		assert.NoError(t, processor.Stop(ctx))
	})
}

func TestProcessorLoopPanicIsolation(t *testing.T) {
	// A panic inside processQueue (here: a nil redisClient deref in
	// DequeueRetry) must not crash the process or kill the loop: the per-tick
	// ProtectErr wrapper recovers it and the loop continues until Stop.
	cfg := DefaultConfig()
	cfg.Interval = 10 * time.Millisecond
	processor := NewProcessor(nil, nil, cfg)
	// Register a collection so processQueue actually reaches DequeueRetry on
	// the nil redisClient, which panics.
	processor.RegisterCollection("users")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	assert.NoError(t, processor.Start(ctx))
	// Let a few ticks run so processQueue panics and is recovered.
	time.Sleep(50 * time.Millisecond)
	assert.NoError(t, processor.Stop(ctx))
}

// successTransport is a mock transport that always succeeds.
type successTransport struct{}

func (s *successTransport) Close() error { return nil }
func (s *successTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	return nil
}

// failingTransport is a mock transport that always fails dispatch.
type failingTransport struct{}

func (s *failingTransport) Close() error { return nil }
func (s *failingTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	return assert.AnError
}

// retryEventKey returns a canonical string for a retry event (its queue member
// JSON), used to detect whether a member was removed.
func retryEventKey(event redis.RetryEvent) string {
	data, _ := json.Marshal(event)
	return string(data)
}

// fakeStore is a map-backed Store used to exercise the retry processor's
// failure and ordering semantics without a live Redis.
type fakeStore struct {
	queue map[string][]redis.RetryEvent

	failEnqueue   bool
	failRemove    bool
	failSendToDLQ bool

	enqueueCalls int
	removeCalls  int
	dlqCalls     int
	dlqByCol     map[string][][]byte
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		queue:    make(map[string][]redis.RetryEvent),
		dlqByCol: make(map[string][][]byte),
	}
}

func (f *fakeStore) DequeueRetry(ctx context.Context, collectionName string, limit int64) ([]redis.RetryEvent, error) {
	events := f.queue[collectionName]
	out := make([]redis.RetryEvent, 0, len(events))
	for _, e := range events {
		if e.NextRetryAt.UnixNano() <= time.Now().UnixNano() {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *fakeStore) EnqueueRetry(ctx context.Context, event redis.RetryEvent) error {
	f.enqueueCalls++
	if f.failEnqueue {
		return assert.AnError
	}
	f.queue[event.CollectionName] = append(f.queue[event.CollectionName], event)
	return nil
}

func (f *fakeStore) RemoveRetryEvent(ctx context.Context, collectionName string, event redis.RetryEvent) error {
	f.removeCalls++
	if f.failRemove {
		return assert.AnError
	}
	key := retryEventKey(event)
	q := f.queue[collectionName]
	for i, e := range q {
		if retryEventKey(e) == key {
			f.queue[collectionName] = append(q[:i], q[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeStore) SendToDLQ(ctx context.Context, collectionName string, event interface{}) error {
	f.dlqCalls++
	if f.failSendToDLQ {
		return assert.AnError
	}
	data, _ := json.Marshal(event)
	f.dlqByCol[collectionName] = append(f.dlqByCol[collectionName], data)
	return nil
}

func (f *fakeStore) GetRetryQueueLength(ctx context.Context, collectionName string) (int64, error) {
	return int64(len(f.queue[collectionName])), nil
}

func (f *fakeStore) GetDLQLength(ctx context.Context, collectionName string) (int64, error) {
	return int64(len(f.dlqByCol[collectionName])), nil
}

func (f *fakeStore) queuedMembers(collectionName string) []string {
	var members []string
	for _, e := range f.queue[collectionName] {
		members = append(members, retryEventKey(e))
	}
	return members
}

func TestProcessRetryEventDispatchFailure(t *testing.T) {
	newDispatcher := func() *dispatch.Dispatcher {
		d := dispatch.NewDispatcher()
		d.Register("users", dispatch.NewRuntimeSink(collections.Sink{}, &failingTransport{}))
		return d
	}

	makeEvent := func(retryCount, maxRetries int, next time.Time) redis.RetryEvent {
		eventData, err := bson.MarshalExtJSON(streams.StreamRecord{
			TableName:  "users",
			RecordType: streams.InsertRecord,
			NewImage:   bson.M{"_id": "123"},
		}, false, false)
		require.NoError(t, err)
		return redis.RetryEvent{
			ID:             "users-123",
			CollectionName: "users",
			EventData:      eventData,
			RetryCount:     retryCount,
			MaxRetries:     maxRetries,
			NextRetryAt:    next,
		}
	}

	ctx := context.Background()
	collectionName := "users"

	t.Run("dispatch fails and enqueue(updated) fails: old event retained, no removal", func(t *testing.T) {
		store := newFakeStore()
		store.failEnqueue = true
		original := makeEvent(0, 5, time.Now().Add(-time.Second))
		store.queue["users"] = []redis.RetryEvent{original}

		p := NewProcessor(store, newDispatcher(), DefaultConfig())
		p.processRetryEvent(ctx, collectionName, original)

		// Old event must still be present, unmodified.
		require.Len(t, store.queue["users"], 1)
		assert.Equal(t, 0, store.queue["users"][0].RetryCount, "retry count must not advance when enqueue fails")
		// Enqueue was attempted, but removal must NOT have been called.
		assert.Equal(t, 1, store.enqueueCalls)
		assert.Equal(t, 0, store.removeCalls)
	})

	t.Run("dispatch fails and enqueue(updated) succeeds: one member with incremented count", func(t *testing.T) {
		store := newFakeStore()
		original := makeEvent(0, 5, time.Now().Add(-time.Second))
		store.queue["users"] = []redis.RetryEvent{original}

		p := NewProcessor(store, newDispatcher(), DefaultConfig())
		p.processRetryEvent(ctx, collectionName, original)

		// Exactly one member remains, with RetryCount incremented and a new
		// NextRetryAt.
		require.Len(t, store.queue["users"], 1)
		assert.Equal(t, 1, store.queue["users"][0].RetryCount)
		assert.True(t, store.queue["users"][0].NextRetryAt.After(original.NextRetryAt),
			"NextRetryAt should be bumped for the next attempt")
		assert.Equal(t, 1, store.enqueueCalls)
		assert.Equal(t, 1, store.removeCalls)
	})

	t.Run("dispatch fails, enqueue succeeds, remove(old) fails: updated event present, stale duplicate tolerated", func(t *testing.T) {
		store := newFakeStore()
		store.failRemove = true
		original := makeEvent(0, 5, time.Now().Add(-time.Second))
		store.queue["users"] = []redis.RetryEvent{original}

		p := NewProcessor(store, newDispatcher(), DefaultConfig())
		p.processRetryEvent(ctx, collectionName, original)

		// The updated event must be enqueued (nothing lost); remove(old) failing
		// means the stale original is also still present -- a duplicate, which is
		// acceptable under at-least-once.
		require.Len(t, store.queue["users"], 2, "updated event enqueued plus stale original")
		var updated *redis.RetryEvent
		for i := range store.queue["users"] {
			if store.queue["users"][i].RetryCount == 1 {
				updated = &store.queue["users"][i]
				break
			}
		}
		require.NotNil(t, updated, "updated event (RetryCount=1) must be present")
		assert.True(t, updated.NextRetryAt.After(original.NextRetryAt), "NextRetryAt should be bumped")
		assert.Equal(t, 1, store.enqueueCalls)
		assert.Equal(t, 1, store.removeCalls, "remove was attempted and failed")
	})

	t.Run("dispatch succeeds: event removed, no enqueue", func(t *testing.T) {
		store := newFakeStore()
		original := makeEvent(0, 5, time.Now().Add(-time.Second))

		dispatcher := dispatch.NewDispatcher()
		dispatcher.Register("users", dispatch.NewRuntimeSink(collections.Sink{}, &successTransport{}))

		p := NewProcessor(store, dispatcher, DefaultConfig())
		p.processRetryEvent(ctx, collectionName, original)

		// Success: the event is settled/delivered so it must be removed from
		// the retry queue, and no re-queue/enqueue happens.
		assert.Equal(t, 0, store.enqueueCalls)
		assert.Equal(t, 1, store.removeCalls)
	})
}

func TestProcessRetryEventMaxRetries(t *testing.T) {
	newDispatcher := func() *dispatch.Dispatcher {
		d := dispatch.NewDispatcher()
		d.Register("users", dispatch.NewRuntimeSink(collections.Sink{}, &successTransport{}))
		return d
	}

	makeEvent := func(retryCount, maxRetries int) redis.RetryEvent {
		return redis.RetryEvent{
			ID:             "users-123",
			CollectionName: "users",
			EventData:      []byte(`{"tableName":"users"}`),
			RetryCount:     retryCount,
			MaxRetries:     maxRetries,
			NextRetryAt:    time.Now().Add(-time.Second),
		}
	}

	ctx := context.Background()
	collectionName := "users"

	t.Run("max retries and SendToDLQ fails: event retained, not removed", func(t *testing.T) {
		store := newFakeStore()
		store.failSendToDLQ = true
		event := makeEvent(5, 5)
		store.queue["users"] = []redis.RetryEvent{event}

		p := NewProcessor(store, newDispatcher(), DefaultConfig())
		p.processRetryEvent(ctx, collectionName, event)

		// DLQ was attempted, but the event must NOT be removed.
		assert.Equal(t, 1, store.dlqCalls)
		assert.Equal(t, 0, store.removeCalls)
		require.Len(t, store.queue["users"], 1, "event must stay queued when DLQ push fails")
	})

	t.Run("max retries and SendToDLQ succeeds: event removed, DLQ payload recorded", func(t *testing.T) {
		store := newFakeStore()
		event := makeEvent(5, 5)
		store.queue["users"] = []redis.RetryEvent{event}

		p := NewProcessor(store, newDispatcher(), DefaultConfig())
		p.processRetryEvent(ctx, collectionName, event)

		assert.Equal(t, 1, store.dlqCalls)
		assert.Equal(t, 1, store.removeCalls)
		assert.Len(t, store.queue["users"], 0, "event removed after successful DLQ push")
		require.Len(t, store.dlqByCol["users"], 1, "DLQ payload must be recorded")
	})
}
