package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sergiors/conduit/internal/collections"
	redisclient "github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestWatcherCreation(t *testing.T) {
	t.Run("new watcher with correct configuration", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", true, "", nil, nil)

		assert.NotNil(t, watcher)
		assert.Equal(t, "conduit", watcher.database)
		assert.Equal(t, "users", watcher.collectionName)
		assert.True(t, watcher.oldImage)
		assert.Equal(t, "", watcher.resumeToken)
	})

	t.Run("watcher with resume token", func(t *testing.T) {
		token := "test-resume-token"
		watcher := NewWatcher(nil, "conduit", "orders", false, token, nil, nil)

		assert.NotNil(t, watcher)
		assert.Equal(t, token, watcher.resumeToken)
	})
}

// TestBuildChangeStreamOptions verifies the resume-position policy for a watch
// session: resume token wins, then the first-start checkpoint, then neither
// (a plain fresh stream). This is the deterministic unit test for the
// first-start event window fix.
func TestBuildChangeStreamOptions(t *testing.T) {
	checkpoint := primitive.Timestamp{T: 1787932086, I: 1}

	t.Run("token present wins over the checkpoint", func(t *testing.T) {
		tokenDoc, err := bson.Marshal(bson.M{"_data": "826A91ADB6000000022B042C0100296E"})
		require.NoError(t, err)
		w := NewWatcher(nil, "conduit", "users", false, string(tokenDoc), &checkpoint, nil)

		opts := w.buildChangeStreamOptions()
		assert.NotNil(t, opts.ResumeAfter, "a resume token must set ResumeAfter")
		// With a token present, the checkpoint must NOT be applied.
		assert.Nil(t, opts.StartAtOperationTime, "the checkpoint must never override a resume token")
	})

	t.Run("no token with a checkpoint sets StartAtOperationTime", func(t *testing.T) {
		w := NewWatcher(nil, "conduit", "users", false, "", &checkpoint, nil)

		opts := w.buildChangeStreamOptions()
		assert.Nil(t, opts.ResumeAfter, "no resume token")
		assert.NotNil(t, opts.StartAtOperationTime, "fresh watcher with a checkpoint must anchor at it")
		assert.Equal(t, checkpoint, *opts.StartAtOperationTime)
	})

	t.Run("no token and no checkpoint sets neither", func(t *testing.T) {
		w := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		opts := w.buildChangeStreamOptions()
		assert.Nil(t, opts.ResumeAfter)
		assert.Nil(t, opts.StartAtOperationTime, "no checkpoint, no token: fresh stream at current position")
	})

	t.Run("unparseable stored token behaves as absent (checkpoint applies)", func(t *testing.T) {
		// A corrupt/invalid stored token string cannot be unmarshalled, so it
		// is treated as absent and the checkpoint applies. (The invalidate
		// path clears the token in Redis, but the in-memory value could be
		// transiently corrupt after a restart.)
		w := NewWatcher(nil, "conduit", "users", false, "not-valid-bson", &checkpoint, nil)

		opts := w.buildChangeStreamOptions()
		assert.Nil(t, opts.ResumeAfter, "unparseable token must not set ResumeAfter")
		assert.NotNil(t, opts.StartAtOperationTime, "checkpoint must apply when the stored token is unparseable")
	})
}

func TestWatcherStats(t *testing.T) {
	t.Run("initial stats are correct", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", false, "", nil, nil)

		stats := watcher.GetStats()
		assert.Zero(t, stats.EventsProcessed)
		assert.Nil(t, stats.LastError)
		assert.True(t, !stats.StartTime.IsZero())
	})

	t.Run("IsRunning returns false before start", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", false, "", nil, nil)
		assert.False(t, watcher.IsRunning())
	})
}

func TestParseChange(t *testing.T) {
	t.Run("parse insert operation", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		change := bson.M{
			"operationType": "insert",
			"fullDocument": bson.M{
				"_id":   "123",
				"name":  "John",
				"email": "john@example.com",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.InsertRecord, record.RecordType)
		assert.Equal(t, "users", record.TableName)
		assert.NotNil(t, record.NewImage)
		assert.Nil(t, record.OldImage)
	})

	t.Run("parse update operation with old image", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "orders", true, "", nil, nil)

		change := bson.M{
			"operationType": "update",
			"fullDocument": bson.M{
				"_id":    "456",
				"status": "shipped",
			},
			"fullDocumentBeforeChange": bson.M{
				"_id":    "456",
				"status": "pending",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.ModifyRecord, record.RecordType)
		assert.NotNil(t, record.NewImage)
		assert.NotNil(t, record.OldImage)
	})

	t.Run("parse delete operation", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "sessions", true, "", nil, nil)

		change := bson.M{
			"operationType": "delete",
			"fullDocumentBeforeChange": bson.M{
				"_id":  "789",
				"user": "john",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.RemoveRecord, record.RecordType)
		assert.Nil(t, record.NewImage)
		assert.NotNil(t, record.OldImage)
	})

	t.Run("parse unknown operation type", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", false, "", nil, nil)

		change := bson.M{
			"operationType": "unknown",
		}

		record, err := watcher.parseChange(change)
		assert.Error(t, err)
		assert.Equal(t, streams.StreamRecord{}, record)
	})

	t.Run("parse missing operation type", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "test", false, "", nil, nil)

		change := bson.M{}

		record, err := watcher.parseChange(change)
		assert.Error(t, err)
		assert.Equal(t, streams.StreamRecord{}, record)
	})

	t.Run("event ID is derived from resume token", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		change := bson.M{
			"_id": bson.M{
				"_data": "826A91ADB6000000022B042C0100296E",
			},
			"operationType": "insert",
			"fullDocument":  bson.M{"_id": "123"},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, "users:826A91ADB6000000022B042C0100296E", record.EventID)
	})

	t.Run("event ID is stable across restarts", func(t *testing.T) {
		// Two independent watcher instances (a restart) parsing the same
		// change event must produce the same event ID.
		change := bson.M{
			"_id": bson.M{
				"_data": "826A91ADB6000000022B042C0100296E",
			},
			"operationType": "update",
			"fullDocument":  bson.M{"_id": "123"},
		}

		first := NewWatcher(nil, "conduit", "orders", false, "", nil, nil)
		second := NewWatcher(nil, "conduit", "orders", false, "", nil, nil)

		r1, err := first.parseChange(change)
		assert.NoError(t, err)
		r2, err := second.parseChange(change)
		assert.NoError(t, err)

		assert.Equal(t, r1.EventID, r2.EventID)
		assert.NotEmpty(t, r1.EventID)
	})

	t.Run("event ID falls back to clusterTime and documentKey", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "orders", false, "", nil, nil)

		change := bson.M{
			"operationType": "update",
			"clusterTime":   primitive.Timestamp{T: 1787932086, I: 4},
			"documentKey":   bson.M{"_id": "456"},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, `orders:1787932086:4:{"_id":"456"}`, record.EventID)
	})

	t.Run("event ID fallback is stable across restarts", func(t *testing.T) {
		change := bson.M{
			"operationType": "delete",
			"clusterTime":   primitive.Timestamp{T: 1787932086, I: 7},
			"documentKey":   bson.M{"_id": "789"},
		}

		first := NewWatcher(nil, "conduit", "sessions", false, "", nil, nil)
		second := NewWatcher(nil, "conduit", "sessions", false, "", nil, nil)

		r1, err := first.parseChange(change)
		assert.NoError(t, err)
		r2, err := second.parseChange(change)
		assert.NoError(t, err)

		assert.Equal(t, r1.EventID, r2.EventID)
		assert.NotEmpty(t, r1.EventID)
	})

	t.Run("different changes produce different event IDs", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		first, err := watcher.parseChange(bson.M{
			"_id":           bson.M{"_data": "826A91ADB6000000022B04"},
			"operationType": "insert",
		})
		assert.NoError(t, err)

		second, err := watcher.parseChange(bson.M{
			"_id":           bson.M{"_data": "826A91ADB6000000032B04"},
			"operationType": "insert",
		})
		assert.NoError(t, err)

		assert.NotEqual(t, first.EventID, second.EventID)
	})

	t.Run("event ID is never derived from time.Now", func(t *testing.T) {
		// Parsing the same change twice within one watcher must also be
		// deterministic (guards against any wall-clock dependency).
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		change := bson.M{
			"_id":           bson.M{"_data": "826A91ADB6000000022B04"},
			"operationType": "insert",
		}

		r1, err := watcher.parseChange(change)
		assert.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
		r2, err := watcher.parseChange(change)
		assert.NoError(t, err)

		assert.Equal(t, r1.EventID, r2.EventID)
	})
}

func TestParseChangeDocumentID(t *testing.T) {
	t.Run("insert with ObjectID _id yields hex", func(t *testing.T) {
		// Identity is the MongoDB `_id` regardless of any pk/sk-style fields
		// present in the full document.
		watcher := NewWatcher(nil, "conduit", "orders", false, "", nil, nil)
		oid := primitive.NewObjectID()

		change := bson.M{
			"operationType": "insert",
			"documentKey":   bson.M{"_id": oid},
			"fullDocument": bson.M{
				"_id":     oid,
				"tenant":  "acme",
				"orderId": "o-42",
			},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, oid.Hex(), record.DocumentID)
	})

	t.Run("insert with string _id is verbatim", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		change := bson.M{
			"operationType": "insert",
			"documentKey":   bson.M{"_id": "user-1"},
			"fullDocument":  bson.M{"_id": "user-1", "name": "John"},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, "user-1", record.DocumentID)
	})

	t.Run("delete with only documentKey yields _id hex", func(t *testing.T) {
		// A delete event without a pre-image still carries documentKey, so the
		// identity is always available.
		watcher := NewWatcher(nil, "conduit", "sessions", false, "", nil, nil)
		oid := primitive.NewObjectID()

		change := bson.M{
			"operationType": "delete",
			"documentKey":   bson.M{"_id": oid},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, streams.RemoveRecord, record.RecordType)
		assert.Equal(t, oid.Hex(), record.DocumentID)
	})

	t.Run("missing documentKey leaves DocumentID empty", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		change := bson.M{
			"operationType": "insert",
			"fullDocument":  bson.M{"name": "John"},
		}

		record, err := watcher.parseChange(change)
		assert.NoError(t, err)
		assert.Equal(t, "", record.DocumentID)
	})
}

func TestIsResumeTokenInvalid(t *testing.T) {
	t.Run("token-fatal server codes are invalidating", func(t *testing.T) {
		// Verified against a live MongoDB 8.x replica set:
		//   9     -> FailedToParse: "resume token string was not a valid hex string"
		//   50811 -> KeyString format error (structurally invalid token)
		//   286   -> ChangeStreamHistoryLost: resume point predates the oplog
		//            window (verified via the server error_codes.yml; a forged
		//            ancient token reproduces the sibling 280/50811 live, and
		//            286 is the NonResumableChangeStreamError the server raises
		//            when the token's history has rolled off the oplog).
		//
		// Note: the oplog-window-miss condition is ALSO seen live as code 280
		// ChangeStreamFatalError with the message "cannot resume stream; the
		// resume token was not found" — covered by the message-matching
		// subtest below. Stale-but-structurally-valid tokens (e.g. after a
		// collection drop) do NOT error at Watch() on MongoDB 8.x; the stream
		// delivers a `drop` event then `invalidate`, handled as terminal
		// conditions. So only token-fatal server codes (parse failures and
		// history-lost) invalidate here.
		for _, code := range []int32{9, 286, 50811} {
			err := mongo.CommandError{Code: code, Message: "synthetic"}
			assert.True(t, isResumeTokenInvalid(err), "code %d should invalidate", code)
		}
	})

	t.Run("history-lost error wrapped or as message still detected", func(t *testing.T) {
		// 286 is matched by its typed code, so it must survive wrapping.
		cmdErr := mongo.CommandError{Code: 286, Message: "ChangeStreamHistoryLost: resume point no longer exists"}
		assert.True(t, isResumeTokenInvalid(cmdErr), "typed 286 must invalidate")

		wrapped := fmt.Errorf("start change stream: %w", cmdErr)
		assert.True(t, isResumeTokenInvalid(wrapped), "wrapped 286 must invalidate")

		// The missing-resume-point condition is delivered by MongoDB 8.2 as
		// code 280 ChangeStreamFatalError with this message (verified live
		// twice: structurally valid forged token whose clusterTime predates
		// the oplog). The typed code 280 alone is a wrapper for several fatal
		// stream conditions and must NOT blanket-match; the message substring
		// is the discriminator.
		twoEighty := mongo.CommandError{
			Code:    280,
			Name:    "ChangeStreamFatalError",
			Message: `Executor error during aggregate command on namespace: test.coll :: caused by :: cannot resume stream; the resume token was not found. {_data: "8200000001000000012B042C0100296E"}`,
		}
		assert.True(t, isResumeTokenInvalid(twoEighty), "280 with missing-resume-token message must invalidate")

		wrappedTwoEighty := fmt.Errorf("start change stream: %w", twoEighty)
		assert.True(t, isResumeTokenInvalid(wrappedTwoEighty), "wrapped 280 missing-resume-token must invalidate")

		// Other 280-wrapped fatal conditions (different message) must NOT
		// invalidate — the code is not matched, only the message.
		otherTwoEighty := mongo.CommandError{Code: 280, Name: "ChangeStreamFatalError", Message: "resume stream not allowed for a different reason"}
		assert.False(t, isResumeTokenInvalid(otherTwoEighty), "280 with an unrelated message must not invalidate")

		// A bare message without any typed code (lost through wrapping) still
		// matches via the string fallback.
		assert.True(t, isResumeTokenInvalid(errors.New("start change stream: cannot resume stream; the resume token was not found")))
	})

	t.Run("wrapped token-fatal errors are still detected", func(t *testing.T) {
		cmdErr := mongo.CommandError{Code: 50811, Message: "KeyString format error: Unknown type: 255"}
		wrapped := fmt.Errorf("start change stream: %w", cmdErr)
		assert.True(t, isResumeTokenInvalid(wrapped))
	})

	t.Run("transient errors never invalidate the token", func(t *testing.T) {
		transient := []error{
			errors.New("connection refused"),
			context.DeadlineExceeded,
			io.EOF,
			mongo.CommandError{Code: 6, Message: "HostUnreachable"},
			mongo.CommandError{Code: 89, Message: "NetworkTimeout"},
			mongo.CommandError{Code: 91, Message: "ShutdownInProgress"},
			mongo.CommandError{Code: 189, Message: "PrimarySteppedDown"},
			mongo.CommandError{Code: 262, Message: "ExceededTimeLimit"},
			mongo.CommandError{Code: 43, Message: "CursorNotFound"},
			fmt.Errorf("start change stream: %w", errors.New("server selection error: context deadline exceeded")),
		}
		for _, err := range transient {
			assert.False(t, isResumeTokenInvalid(err), "error %v must not invalidate", err)
		}
	})

	t.Run("structurally valid tokens are never classified invalid", func(t *testing.T) {
		// A stale token from a dropped collection is structurally valid; on
		// MongoDB 8.x it does not error at Watch() — the stream delivers
		// `drop` then `invalidate` instead (verified live). It must not be
		// classified as token-invalid, so the token survives for a future
		// recreate of the same collection.
		validShape := bson.M{"_data": "826A91ADB6000000022B042C0100296E5A1004210D2489479F4AF0A8FC2ABF942361A6463C6F7065726174696F6E54797065003C696E7365727400000004"}
		stored, err := bson.Marshal(validShape)
		assert.NoError(t, err)
		assert.False(t, isResumeTokenInvalid(fmt.Errorf("start change stream: %w", errStaleTokenScenario(stored))))
	})

	t.Run("nil error is not invalidating", func(t *testing.T) {
		assert.False(t, isResumeTokenInvalid(nil))
	})
}

// errStaleTokenScenario wraps a sentinel so the classifier sees a generic
// error carrying a token-looking message; it must still not invalidate.
func errStaleTokenScenario(token []byte) error {
	return errors.New("change stream error for token " + string(token))
}

func TestResumeTokenPreservation(t *testing.T) {
	t.Run("terminal parse errors propagate out of watchOnce", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		// A drop event must surface from parseChange instead of being
		// swallowed, so watchLoop can stop the watcher.
		_, err := watcher.parseChange(bson.M{"operationType": "drop"})
		assert.ErrorIs(t, err, errCollectionDropped)

		_, err = watcher.parseChange(bson.M{"operationType": "invalidate"})
		assert.ErrorIs(t, err, errChangeStreamInvalidated)
	})

	t.Run("sentinel errors are stable and distinct", func(t *testing.T) {
		assert.NotEqual(t, errCollectionDropped, errChangeStreamInvalidated)
		assert.EqualError(t, errCollectionDropped, "collection dropped")
		assert.EqualError(t, errChangeStreamInvalidated, "change stream invalidated")
	})
}

func TestManagerCreation(t *testing.T) {
	t.Run("new manager with correct configuration", func(t *testing.T) {
		cfg := DefaultConfig()
		manager := NewManager(nil, "conduit", nil, nil, nil, nil, cfg)

		assert.NotNil(t, manager)
		assert.Equal(t, "conduit", manager.database)
		assert.Equal(t, 30*time.Second, manager.syncInterval)
		assert.Equal(t, 0, manager.GetActiveWatchers())
	})
}

func TestSinkIdentity(t *testing.T) {
	t.Run("sink identity is the persisted ID", func(t *testing.T) {
		sink := collections.Sink{
			ID:   "507f1f77bcf86cd799439011",
			Type: collections.SinkTypeHTTP,
			Spec: map[string]interface{}{"endpoint": "https://webhook.example.com"},
		}
		assert.Equal(t, "507f1f77bcf86cd799439011", sink.Identity())
	})
}

func TestManagerConfig(t *testing.T) {
	t.Run("default config has sensible values", func(t *testing.T) {
		cfg := DefaultConfig()
		assert.Equal(t, 30*time.Second, cfg.SyncInterval)
	})

	t.Run("custom config", func(t *testing.T) {
		cfg := Config{
			SyncInterval: 60 * time.Second,
		}
		assert.Equal(t, 60*time.Second, cfg.SyncInterval)
	})
}

func TestManagerMarkProcessedTTL(t *testing.T) {
	t.Run("handleEvent marks processed with the fixed 24h TTL", func(t *testing.T) {
		fr := newFakeRedis()
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{}, nil, Config{})
		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

		err := manager.handleEvent(context.Background(), "users", record)

		assert.NoError(t, err)
		assert.Equal(t, 1, fr.markCalls)
		assert.Equal(t, 24*time.Hour, fr.lastMarkTTL)
	})
}

func TestManagerStopIdempotent(t *testing.T) {
	t.Run("Stop before Start is safe", func(t *testing.T) {
		manager := NewManager(nil, "conduit", nil, nil, nil, nil, DefaultConfig())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		assert.NoError(t, manager.Stop(ctx))
	})

	t.Run("Stop twice returns nil", func(t *testing.T) {
		manager := NewManager(nil, "conduit", nil, nil, nil, nil, DefaultConfig())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// Both calls are no-ops (never started), exercising the stopped-flag
		// guard and the nil pubsub path.
		assert.NoError(t, manager.Stop(ctx))
		assert.NoError(t, manager.Stop(ctx))
	})
}

func TestWatcherStopIdempotent(t *testing.T) {
	t.Run("Stop on non-started watcher returns nil", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		assert.NoError(t, watcher.Stop(ctx))
	})

	t.Run("Start then immediate Stop completes without hanging", func(t *testing.T) {
		// A nil *mongo.Client would panic in watchOnce (w.mongoClient.Database
		// on nil) if the goroutine reaches it before Stop cancels the context,
		// so use a real lazily-connected client instead. The v1 driver's
		// Connect is lazy (no network I/O at connect); pointing it at an
		// unroutable address with a short server-selection timeout makes
		// coll.Watch fail fast and deterministically, so watchOnce returns a
		// "start change stream" error and watchLoop's retry select returns on
		// w.ctx.Done once Stop cancels it. This exercises the no-hang behavior
		// under test without a live MongoDB.
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

		watcher := NewWatcher(client, "conduit", "users", false, "", nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		assert.NoError(t, watcher.Start(ctx, func(streams.StreamRecord) error { return nil }))
		assert.NoError(t, watcher.Stop(ctx))
		// Second Stop is a no-op.
		assert.NoError(t, watcher.Stop(ctx))
	})
}

func TestManagerStartWatcherDerivesFromRunCtx(t *testing.T) {
	// A watcher created via manager.startWatcher must derive its context from
	// the manager's run context, so Manager.Stop's runCancel() tears it down
	// even if the watcher's own Stop is never called explicitly. This closes
	// the post-Stop creation race where a concurrent configChangeLoop could
	// otherwise create a watcher that outlives the manager.
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

	manager := NewManager(client, "conduit", nil, nil, nil, nil, DefaultConfig())
	manager.runCtx, manager.runCancel = context.WithCancel(context.Background())

	// startWatcher with a nil redisClient is tolerable here: watchOnce only
	// touches redisClient AFTER a successful event, and with server-selection
	// failures it errors before any redis use.
	err = manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: "users"})
	assert.NoError(t, err)

	watcher, ok := manager.watchers["users"]
	assert.True(t, ok, "watcher should be registered")
	assert.True(t, watcher.IsRunning())

	// Cancelling the run context must make the watcher's Watch call abort
	// rather than hang; Stop then returns promptly.
	manager.runCancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	assert.NoError(t, watcher.Stop(stopCtx))
	assert.False(t, watcher.IsRunning())

	// After the run context is cancelled, startWatcher must refuse to create a
	// new watcher (closing the post-Stop creation race).
	err = manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: "orders"})
	assert.Error(t, err)
	_, exists := manager.watchers["orders"]
	assert.False(t, exists, "no watcher should be created after the run context is cancelled")
}

// newStartWatcherMongoClient returns a lazily-connected mongo client pointed at
// an unroutable address with a short server-selection timeout, so watchOnce
// fails fast on connect instead of hanging or panicking on a nil client. It is
// shared by the startWatcher resume-token tests.
func newStartWatcherMongoClient(t *testing.T) *mongo.Client {
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

// TestManagerStartWatcherResumeTokenFailure verifies that a resume-token read
// error aborts startWatcher with no side effects: no watcher is registered,
// no sinks are captured, and the error is returned wrapped. The manager's
// reconciliation (syncLoop / configChangeLoop) retries startWatcher later, so
// nothing durable may be left behind for that retry.
func TestManagerStartWatcherResumeTokenFailure(t *testing.T) {
	client := newStartWatcherMongoClient(t)
	fr := newFakeRedis()

	// A sentinel representing a transient Redis outage (timeout / failover).
	fr.getErr = errors.New("redis down")

	manager := NewManager(client, "conduit", nil, fr, nil, nil, DefaultConfig())
	manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
	t.Cleanup(manager.runCancel)

	err := manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: "users"})

	// The read error is propagated (wrapped), not swallowed.
	assert.Error(t, err)
	assert.ErrorContains(t, err, "redis down")

	// No side effects for the next reconciliation attempt.
	mgr := manager
	mgr.mu.Lock()
	_, watcherExists := mgr.watchers["users"]
	_, sinkExists := mgr.currentSinks["users"]
	mgr.mu.Unlock()
	assert.False(t, watcherExists, "no watcher may be registered on resume-token failure")
	assert.False(t, sinkExists, "no currentSinks entry may be written on resume-token failure")
}

func TestManagerStartWatcherResumeToken(t *testing.T) {
	t.Run("existing token is used to resume the stream", func(t *testing.T) {
		client := newStartWatcherMongoClient(t)
		fr := newFakeRedis()
		token := "existing-resume-token"
		fr.resumeTokens["users"] = token

		manager := NewManager(client, "conduit", nil, fr, nil, nil, DefaultConfig())
		manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
		t.Cleanup(manager.runCancel)

		err := manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: "users"})
		assert.NoError(t, err)

		watcher, ok := manager.watchers["users"]
		assert.True(t, ok, "watcher should be registered")
		// The token must reach the watcher so watcher.go can SetResumeAfter it.
		assert.Equal(t, token, watcher.resumeToken)
	})

	t.Run("missing token (redis.Nil as empty) starts a fresh stream", func(t *testing.T) {
		client := newStartWatcherMongoClient(t)
		fr := newFakeRedis()

		manager := NewManager(client, "conduit", nil, fr, nil, nil, DefaultConfig())
		manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
		t.Cleanup(manager.runCancel)

		err := manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: "users"})
		assert.NoError(t, err)

		watcher, ok := manager.watchers["users"]
		assert.True(t, ok, "watcher should be registered")
		// Empty token means no resumeAfter: a brand new change stream.
		assert.Equal(t, "", watcher.resumeToken)
	})

	t.Run("first-start checkpoint is passed to a fresh (no-token) watcher", func(t *testing.T) {
		client := newStartWatcherMongoClient(t)
		fr := newFakeRedis()
		checkpoint := primitive.Timestamp{T: uint32(time.Now().Unix()), I: 1}

		manager := NewManager(client, "conduit", nil, fr, nil, nil, DefaultConfig())
		manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
		t.Cleanup(manager.runCancel)

		// A freshly-enabled collection carries StreamStartedAt and, because the
		// worker has never started it before, no resume token in Redis.
		err := manager.startWatcher(manager.runCtx, collections.Collection{
			CollectionName:  "users",
			StreamStartedAt: &checkpoint,
		})
		assert.NoError(t, err)

		watcher, ok := manager.watchers["users"]
		assert.True(t, ok, "watcher should be registered")
		assert.Equal(t, "", watcher.resumeToken, "no token yet")
		assert.NotNil(t, watcher.startAtOperationTime, "checkpoint must reach the watcher")
		assert.Equal(t, checkpoint, *watcher.startAtOperationTime)
	})

	t.Run("nil redis client skips the token read and still starts", func(t *testing.T) {
		client := newStartWatcherMongoClient(t)

		manager := NewManager(client, "conduit", nil, nil, nil, nil, DefaultConfig())
		manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
		t.Cleanup(manager.runCancel)

		err := manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: "users"})
		assert.NoError(t, err)

		watcher, ok := manager.watchers["users"]
		assert.True(t, ok, "watcher should be registered")
		assert.Equal(t, "", watcher.resumeToken)
	})
}

func TestInvokeHandlerPanicIsolation(t *testing.T) {
	t.Run("a panicking handler is converted to an error", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}
		err := watcher.invokeHandler(func(streams.StreamRecord) error {
			var m map[string]int
			m["key"] = 1 // nil map write panics
			return nil
		}, record)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handler panic")
	})

	t.Run("a normal handler error passes through unchanged", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)
		sentinel := errors.New("dispatch failed")

		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}
		err := watcher.invokeHandler(func(streams.StreamRecord) error {
			return sentinel
		}, record)

		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("a clean handler returns nil", func(t *testing.T) {
		watcher := NewWatcher(nil, "conduit", "users", false, "", nil, nil)

		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}
		err := watcher.invokeHandler(func(streams.StreamRecord) error {
			return nil
		}, record)

		assert.NoError(t, err)
	})
}

// fakeRedis is an in-memory RedisClient fake recording resume-token and
// retry-enqueue activity so settlement tests can assert what the watcher and
// manager persisted.
type fakeRedis struct {
	mu            sync.Mutex
	getErr        error
	resumeTokens  map[string]string
	resumeCalls   int
	processed     map[string]bool
	markCalls     int
	lastMarkTTL   time.Duration
	enqueueCalls  int
	enqueueResult error
	lastEnqueued  redisclient.RetryEvent
	subscribeErr  error
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		resumeTokens: make(map[string]string),
		processed:    make(map[string]bool),
	}
}

func (f *fakeRedis) GetResumeToken(ctx context.Context, collectionName string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.resumeTokens[collectionName], nil
}

func (f *fakeRedis) SetResumeToken(ctx context.Context, collectionName, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeCalls++
	if f.getErr != nil {
		return f.getErr
	}
	f.resumeTokens[collectionName] = token
	return nil
}

func (f *fakeRedis) DeleteResumeToken(ctx context.Context, collectionName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.resumeTokens, collectionName)
	return nil
}

func (f *fakeRedis) IsProcessed(ctx context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.processed[id], nil
}

func (f *fakeRedis) MarkProcessed(ctx context.Context, id string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCalls++
	f.lastMarkTTL = ttl
	f.processed[id] = true
	return nil
}

func (f *fakeRedis) EnqueueRetry(ctx context.Context, event redisclient.RetryEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueueCalls++
	f.lastEnqueued = event
	return f.enqueueResult
}

func (f *fakeRedis) SubscribeConfigChanges(ctx context.Context) (*redis.PubSub, error) {
	return nil, f.subscribeErr
}

func (f *fakeRedis) resumeToken(collectionName string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeTokens[collectionName]
}

// newProcessEventWatcher returns a watcher ready to run processEvent directly:
// Start normally sets w.ctx, so set it here to exercise the detached-context
// token persistence without spawning the watch goroutine.
func newProcessEventWatcher(redisClient RedisClient) *Watcher {
	watcher := NewWatcher(nil, "conduit", "users", false, "", nil, redisClient)
	watcher.ctx = context.Background()
	return watcher
}

// processEvent calls the watcher's settlement path directly with a synthetic
// resume token, mirroring what watchOnce does per change event.
func TestProcessEventSettlement(t *testing.T) {
	// A real resume token is a bson.Raw document shaped like
	// {"_data": "<hex>"}; marshalling a bson.M yields a raw byte slice.
	tokenDoc, err := bson.Marshal(bson.M{"_data": "826A91ADB6000000022B042C0100296E"})
	assert.NoError(t, err)
	var token bson.Raw = tokenDoc

	t.Run("handler returns nil advances the token and persists it", func(t *testing.T) {
		fr := newFakeRedis()
		watcher := newProcessEventWatcher(fr)
		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

		err := watcher.processEvent(func(streams.StreamRecord) error { return nil }, record, token)

		assert.NoError(t, err)
		assert.NotEqual(t, "", watcher.resumeToken, "in-memory token must advance")
		assert.Equal(t, 1, fr.resumeCalls, "SetResumeToken must be attempted")
		assert.Equal(t, watcher.resumeToken, fr.resumeToken("users"))
		assert.Equal(t, int64(1), watcher.GetStats().EventsProcessed)
	})

	t.Run("handler returns an error does not advance the token", func(t *testing.T) {
		fr := newFakeRedis()
		watcher := newProcessEventWatcher(fr)
		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

		sentinel := errors.New("dispatch failed and enqueue failed")
		err := watcher.processEvent(func(streams.StreamRecord) error { return sentinel }, record, token)

		assert.Error(t, err)
		assert.ErrorIs(t, err, sentinel)
		assert.Equal(t, "", watcher.resumeToken, "in-memory token must NOT advance")
		assert.Equal(t, 0, fr.resumeCalls, "SetResumeToken must NOT be called")
		assert.Equal(t, "", fr.resumeToken("users"))
		assert.Equal(t, int64(0), watcher.GetStats().EventsProcessed)
	})

	t.Run("handler panic is converted to an error and does not advance the token", func(t *testing.T) {
		fr := newFakeRedis()
		watcher := newProcessEventWatcher(fr)
		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

		err := watcher.processEvent(func(streams.StreamRecord) error {
			var m map[string]int
			m["key"] = 1 // nil map write panics
			return nil
		}, record, token)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handler panic")
		assert.Equal(t, "", watcher.resumeToken, "in-memory token must NOT advance")
		assert.Equal(t, 0, fr.resumeCalls, "SetResumeToken must NOT be called")
	})

	t.Run("after an unsettled event the next settled event advances the token", func(t *testing.T) {
		fr := newFakeRedis()
		watcher := newProcessEventWatcher(fr)

		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

		// First event is unsettled.
		unsettled := errors.New("dispatch failed and enqueue failed")
		err := watcher.processEvent(func(streams.StreamRecord) error { return unsettled }, record, token)
		assert.Error(t, err)
		assert.Equal(t, "", watcher.resumeToken)

		// The watch loop would reopen the stream from the (unchanged) last
		// settled token — the same event is re-parsed and now succeeds.
		err = watcher.processEvent(func(streams.StreamRecord) error { return nil }, record, token)
		assert.NoError(t, err)
		assert.NotEqual(t, "", watcher.resumeToken, "token must advance once the event is settled")
		assert.Equal(t, 1, fr.resumeCalls)
		assert.Equal(t, int64(1), watcher.GetStats().EventsProcessed)
	})

	t.Run("nil resume token still counts a settled event", func(t *testing.T) {
		fr := newFakeRedis()
		watcher := newProcessEventWatcher(fr)
		record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

		err := watcher.processEvent(func(streams.StreamRecord) error { return nil }, record, nil)

		assert.NoError(t, err)
		assert.Equal(t, int64(1), watcher.GetStats().EventsProcessed)
		assert.Equal(t, 0, fr.resumeCalls)
	})
}

// fakeDispatcher records dispatch calls and returns a configurable error.
type fakeDispatcher struct {
	dispatchErr error
	calls       int
}

func (d *fakeDispatcher) Dispatch(ctx context.Context, collectioName string, record streams.StreamRecord) error {
	d.calls++
	return d.dispatchErr
}

// TestHandleEventSettlement verifies the manager-level settlement contract:
// dispatch failure + enqueue success settles the event (nil), while dispatch
// failure + enqueue failure returns the ErrEventUnsettled signal.
func TestHandleEventSettlement(t *testing.T) {
	record := streams.StreamRecord{TableName: "users", EventID: "users:abc"}

	t.Run("dispatch succeeds returns nil", func(t *testing.T) {
		fr := newFakeRedis()
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{}, nil, DefaultConfig())

		err := manager.handleEvent(context.Background(), "users", record)

		assert.NoError(t, err)
		assert.Equal(t, 1, fr.markCalls, "successful dispatch marks the event processed")
		assert.Equal(t, 0, fr.enqueueCalls)
	})

	t.Run("dispatch fails but enqueue succeeds returns nil", func(t *testing.T) {
		fr := newFakeRedis()
		dispatchErr := errors.New("sink down")
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{dispatchErr: dispatchErr}, nil, DefaultConfig())

		err := manager.handleEvent(context.Background(), "users", record)

		assert.NoError(t, err, "event durably queued for retry is settled")
		assert.Equal(t, 1, fr.enqueueCalls)
		assert.Equal(t, "users:abc", fr.lastEnqueued.ID,
			"retry ID must use the eventID directly, not collectionName:eventID")
		assert.Equal(t, 0, fr.markCalls, "a queued event is not yet delivered, so not marked processed")
	})

	t.Run("dispatch fails and enqueue fails returns ErrEventUnsettled", func(t *testing.T) {
		fr := newFakeRedis()
		fr.enqueueResult = errors.New("redis down")
		dispatchErr := errors.New("sink down")
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{dispatchErr: dispatchErr}, nil, DefaultConfig())

		err := manager.handleEvent(context.Background(), "users", record)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrEventUnsettled)
		assert.Equal(t, 1, fr.enqueueCalls)
		assert.Equal(t, 0, fr.markCalls)
	})

	t.Run("idempotency-skip returns nil (settled in a previous attempt)", func(t *testing.T) {
		fr := newFakeRedis()
		fr.processed[record.EventID] = true
		manager := NewManager(nil, "conduit", nil, fr, &fakeDispatcher{}, nil, DefaultConfig())

		err := manager.handleEvent(context.Background(), "users", record)

		assert.NoError(t, err, "idempotent skip is settled: the event was dispatched in a previous attempt")
		assert.Equal(t, 0, fr.markCalls)
		assert.Equal(t, 0, fr.enqueueCalls)
	})
}

func TestWatcherStartPanicHandlerDoesNotCrash(t *testing.T) {
	// A handler that panics must not crash the test binary: the per-event
	// isolation converts it to an error, and the goroutine backstop contains
	// anything that slips past. Use a real lazily-connected client so
	// watchOnce fails fast on server selection rather than panicking on a nil
	// client deref.
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

	watcher := NewWatcher(client, "conduit", "users", false, "", nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	assert.NoError(t, watcher.Start(ctx, func(streams.StreamRecord) error {
		var m map[string]int
		m["key"] = 1 // nil map write panics
		return nil
	}))
	assert.NoError(t, watcher.Stop(ctx))
	// After Stop the goroutine has exited; the backstop must have cleared
	// isRunning so IsRunning no longer lies about a dead watcher.
	assert.Eventually(t, func() bool { return !watcher.IsRunning() }, 2*time.Second, 10*time.Millisecond)
}

// newLiveMongoClient connects to the local replica set used by the compose
// stack. It skips the test if MongoDB is not reachable, so the recovery tests
// degrade gracefully on machines without the stack running.
func newLiveMongoClient(t *testing.T) *mongo.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017/?directConnection=true"))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client
}

// TestSyncWithCollectionsRecreatesDeadWatcher verifies the manager's
// reconciliation recovers a registered-but-dead watcher. Both lifecycle paths
// that leave a dead watcher behind — the panic path and the terminal-exit path
// (collection dropped / change stream invalidated) — now converge on
// IsRunning()==false, which syncWithCollections must treat as "watcher absent"
// and recreate. The test simulates each path by flipping the internal isRunning
// flag on a live watcher, then asserting sync replaces it with a fresh running
// instance.
func TestSyncWithCollectionsRecreatesDeadWatcher(t *testing.T) {
	client := newLiveMongoClient(t)
	fr := newFakeRedis()

	const db = "conduit_test_lifecycle"
	settings := collections.NewManager(client, db)

	names := []string{"recovery_test", "recovery_test2"}
	cleanup := func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := settings.Get(bgCtx, name); err == nil {
				_ = settings.DisableDeletionProtection(bgCtx, name)
				_ = settings.Delete(bgCtx, name)
			}
		}
	}
	cleanup() // remove leftovers from a previous run
	t.Cleanup(cleanup)

	// Create two stream-enabled collection configs.
	for _, name := range names {
		require.NoError(t, settings.Create(context.Background(), &collections.Collection{
			CollectionName: name,
			StreamEnabled:  true,
		}))
	}

	manager := NewManager(client, db, settings, fr, nil, nil, DefaultConfig())
	manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
	t.Cleanup(manager.runCancel)

	// Start both watchers; each is alive (IsRunning true).
	for _, name := range names {
		require.NoError(t, manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: name}))
	}
	first := manager.watchers[names[0]]
	second := manager.watchers[names[1]]
	require.True(t, first.IsRunning())
	require.True(t, second.IsRunning())

	// Simulate a dead watcher: the panic path (isRunning flipped false) and the
	// terminal-exit path (isRunning false, watcher still registered). Both now
	// converge on IsRunning()==false, which sync must treat as "absent".
	first.isRunning.Store(false)
	second.isRunning.Store(false)

	manager.syncWithCollections(context.Background())

	// Both watchers must have been recreated: fresh instances, running again.
	recovered1 := manager.watchers[names[0]]
	recovered2 := manager.watchers[names[1]]
	require.NotNil(t, recovered1)
	require.NotNil(t, recovered2)
	assert.NotSame(t, first, recovered1, "%s watcher must be a fresh instance", names[0])
	assert.NotSame(t, second, recovered2, "%s watcher must be a fresh instance", names[1])
	assert.True(t, recovered1.IsRunning(), "recreated watcher must be running")
	assert.True(t, recovered2.IsRunning(), "recreated watcher must be running")
}

// TestSyncWithCollectionsIdempotent verifies that running syncWithCollections
// twice in a row on a healthy setup is a no-op: the watcher is not duplicated
// (impossible by map construction) and, crucially, is not needlessly recreated —
// the same pointer survives both syncs.
func TestSyncWithCollectionsIdempotent(t *testing.T) {
	client := newLiveMongoClient(t)
	fr := newFakeRedis()

	const db = "conduit_test_lifecycle"
	settings := collections.NewManager(client, db)

	const name = "recovery_test"
	cleanup := func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := settings.Get(bgCtx, name); err == nil {
			_ = settings.DisableDeletionProtection(bgCtx, name)
			_ = settings.Delete(bgCtx, name)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	require.NoError(t, settings.Create(context.Background(), &collections.Collection{
		CollectionName: name,
		StreamEnabled:  true,
	}))

	manager := NewManager(client, db, settings, fr, nil, nil, DefaultConfig())
	manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
	t.Cleanup(manager.runCancel)

	require.NoError(t, manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: name}))
	original := manager.watchers[name]

	// Two healthy syncs must not duplicate or restart the watcher.
	manager.syncWithCollections(context.Background())
	manager.syncWithCollections(context.Background())

	require.Len(t, manager.watchers, 1)
	assert.Same(t, original, manager.watchers[name], "healthy watcher must not be recreated")
	assert.True(t, manager.watchers[name].IsRunning())
}

// TestHandleCollectionChangeDeletedStopsWatcher verifies that a config-change
// notification for a collection whose config document is gone (i.e. DELETED)
// stops the watcher immediately, instead of logging a fetch error and leaving
// the CDC stream running until the next sync cycle. It also asserts the
// not-found path does not touch other collections, and that a stream-disabled
// (but existing) collection still just stops its watcher.
func TestHandleCollectionChangeDeletedStopsWatcher(t *testing.T) {
	client := newLiveMongoClient(t)
	fr := newFakeRedis()

	const db = "conduit_test_handlechange"
	settings := collections.NewManager(client, db)

	names := []string{"gone_coll", "kept_coll", "disabled_coll"}
	cleanup := func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range names {
			if _, err := settings.Get(bgCtx, name); err == nil {
				_ = settings.DisableDeletionProtection(bgCtx, name)
				_ = settings.Delete(bgCtx, name)
			}
		}
	}
	cleanup() // remove leftovers from a previous run
	t.Cleanup(cleanup)

	// Create three stream-enabled collection configs.
	for _, name := range names {
		require.NoError(t, settings.Create(context.Background(), &collections.Collection{
			CollectionName: name,
			StreamEnabled:  true,
		}))
	}

	manager := NewManager(client, db, settings, fr, nil, nil, DefaultConfig())
	manager.runCtx, manager.runCancel = context.WithCancel(context.Background())
	t.Cleanup(manager.runCancel)

	// Start watchers for all three collections.
	for _, name := range names {
		require.NoError(t, manager.startWatcher(manager.runCtx, collections.Collection{CollectionName: name}))
	}
	require.Len(t, manager.watchers, 3)

	t.Run("deleted collection stops its watcher and leaves others alone", func(t *testing.T) {
		// Delete "gone_coll": disable deletion protection, then Delete removes
		// the config document (and fires OnPublish, which the worker consumes
		// as a config-change for a name that no longer exists).
		require.NoError(t, settings.DisableDeletionProtection(context.Background(), "gone_coll"))
		require.NoError(t, settings.Delete(context.Background(), "gone_coll"))

		// The config document is gone, so Get returns ErrCollectionNotFound.
		_, err := settings.Get(context.Background(), "gone_coll")
		require.ErrorIs(t, err, collections.ErrCollectionNotFound)

		manager.handleCollectionChange(context.Background(), "gone_coll")

		// The deleted collection's watcher must be removed immediately.
		manager.mu.RLock()
		_, goneExists := manager.watchers["gone_coll"]
		_, keptExists := manager.watchers["kept_coll"]
		manager.mu.RUnlock()
		assert.False(t, goneExists, "deleted collection's watcher must be stopped")
		assert.True(t, keptExists, "unrelated collection's watcher must be untouched")
	})

	t.Run("stream-disabled collection still just stops its watcher", func(t *testing.T) {
		// DisableStream keeps the config document (streamEnabled=false) but
		// fires OnPublish; handleCollectionChange must stop the watcher without
		// purging state (nothing observable in fakeRedis beyond no panic).
		require.NoError(t, settings.DisableStream(context.Background(), "disabled_coll"))

		manager.handleCollectionChange(context.Background(), "disabled_coll")

		manager.mu.RLock()
		_, disabledExists := manager.watchers["disabled_coll"]
		manager.mu.RUnlock()
		assert.False(t, disabledExists, "stream-disabled collection's watcher must be stopped")
	})
}

// TestWatcherTerminalExitClearsIsRunning verifies the flag-no-longer-lies
// contract for ALL exit paths: cancelling the watcher's own context (as Stop
// would) makes the watch goroutine exit, and the Start backstop clears
// isRunning WITHOUT Stop being called. This is the deterministic regression for
// the terminal-exit path (drop/invalidate) that previously left IsRunning()
// reporting true on a dead watcher.
// TestFirstStartCheckpointLiveStreamsPreStartEvents is the live end-to-end
// verification for the first-start event window fix. It proves that a freshly
// enabled collection whose checkpoint (streamStartedAt) is BEFORE some
// writes will, when the watcher opens a stream anchored at that checkpoint with
// NO resume token, deliver those pre-start writes (they are not skipped). This
// is exactly the enable → watcher-start gap the fix closes.
func TestFirstStartCheckpointLiveStreamsPreStartEvents(t *testing.T) {
	client := newLiveMongoClient(t)
	fr := newFakeRedis()

	const db = "conduit_test_firststart"
	// Use the collections manager to capture a real streamStartedAt via
	// EnableStream, then simulate the gap: write events BEFORE starting a
	// watcher anchored at that checkpoint, and assert the watcher observes them.
	settings := collections.NewManager(client, db)

	const name = "firststart_coll"
	cleanup := func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := settings.Get(bgCtx, name); err == nil {
			_ = settings.DisableDeletionProtection(bgCtx, name)
			_ = settings.Delete(bgCtx, name)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	require.NoError(t, settings.Create(context.Background(), &collections.Collection{
		CollectionName: name,
	}))
	// The checkpoint is captured here, at enablement.
	require.NoError(t, settings.EnableStream(context.Background(), name, false))

	enabled, err := settings.Get(context.Background(), name)
	require.NoError(t, err)
	require.NotNil(t, enabled.StreamStartedAt, "EnableStream must record a checkpoint")

	// Simulate the enable → watcher-start window: write a document that happens
	// AFTER the checkpoint but BEFORE the watcher is started. Without the fix
	// this write is skipped (the stream would start at "now").
	physical := client.Database(db).Collection(name)
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer writeCancel()
	_, err = physical.InsertOne(writeCtx, bson.M{"_id": "pre-start-doc", "v": 1})
	require.NoError(t, err)

	// Build the watcher anchored at the checkpoint (no resume token), exactly
	// as the manager's startWatcher does for a freshly-enabled collection.
	watcher := NewWatcher(client, db, name, false, "", enabled.StreamStartedAt, fr)

	// The manager's handler settles every event: mark processed + dispatch.
	observed := make(chan string, 8)
	started := make(chan struct{})
	startErr := make(chan error, 1)
	go func() {
		startErr <- watcher.Start(context.Background(), func(record streams.StreamRecord) error {
			observed <- record.DocumentID
			return nil
		})
		close(started)
	}()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = watcher.Stop(cctx)
	})

	// MongoDB's change stream, when anchored at a startAtOperationTime that
	// does not correspond to an exact oplog entry, synthesizes an `init`
	// anchor (a phantom INSERT and DELETE of _id "init") to establish the
	// resume point before replaying real events. Loop until the real pre-start
	// document is observed, ignoring the phantom anchor.
	sawTarget := false
	deadline := time.After(25 * time.Second)
	for !sawTarget {
		select {
		case id := <-observed:
			if id == "pre-start-doc" {
				sawTarget = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for the pre-start write to be delivered")
		}
	}
	assert.True(t, sawTarget, "the pre-watcher-start write must be delivered (not skipped)")

	// The handler returned nil (event settled), so the resume token must have
	// advanced and been persisted — evidence the event was actually processed,
	// not merely observed on the stream.
	require.Eventually(t, func() bool {
		return fr.resumeToken(name) != ""
	}, 5*time.Second, 20*time.Millisecond, "the settled event must advance and persist the resume token")
	select {
	case err := <-startErr:
		_ = err
	default:
	}
}

func TestWatcherTerminalExitClearsIsRunning(t *testing.T) {
	client := newStartWatcherMongoClient(t)
	watcher := NewWatcher(client, "conduit", "users", false, "", nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, watcher.Start(ctx, func(streams.StreamRecord) error { return nil }))
	require.True(t, watcher.IsRunning())

	// Cancel the watcher's own context directly (as Stop would) WITHOUT calling
	// Stop. The goroutine exits on ctx.Done and the backstop must clear
	// isRunning, so IsRunning no longer lies about a dead watcher.
	watcher.cancel()

	assert.Eventually(t, func() bool { return !watcher.IsRunning() }, 2*time.Second, 10*time.Millisecond)
}

// TestPersistTerminalToken verifies that a terminal drop/invalidate event's
// own resume token is persisted (in-memory + best-effort Redis) so the next
// session resumes after it instead of replaying it forever.
func TestPersistTerminalToken(t *testing.T) {
	tokenDoc, err := bson.Marshal(bson.M{"_data": "826A95F30D000000022B042C0100296E"})
	require.NoError(t, err)

	t.Run("updates in-memory token and persists to redis", func(t *testing.T) {
		fr := newFakeRedis()
		w := NewWatcher(nil, "conduit", "users", false, "old-token", nil, fr)
		w.ctx = context.Background()

		w.persistTerminalToken(tokenDoc)

		assert.Equal(t, string(tokenDoc), w.resumeToken, "in-memory token must advance")
		assert.Equal(t, string(tokenDoc), fr.resumeTokens["users"], "redis token must advance")
	})

	t.Run("redis failure still updates in-memory token", func(t *testing.T) {
		fr := newFakeRedis()
		fr.getErr = errors.New("redis down")
		w := NewWatcher(nil, "conduit", "users", false, "old-token", nil, fr)
		w.ctx = context.Background()

		w.persistTerminalToken(tokenDoc)

		assert.Equal(t, string(tokenDoc), w.resumeToken, "best-effort: in-memory token still advances")
	})

	t.Run("nil redis client is tolerated", func(t *testing.T) {
		w := NewWatcher(nil, "conduit", "users", false, "old-token", nil, nil)
		w.ctx = context.Background()

		w.persistTerminalToken(tokenDoc)

		assert.Equal(t, string(tokenDoc), w.resumeToken)
	})

	t.Run("nil token is a no-op", func(t *testing.T) {
		fr := newFakeRedis()
		w := NewWatcher(nil, "conduit", "users", false, "old-token", nil, fr)
		w.ctx = context.Background()

		w.persistTerminalToken(nil)

		assert.Equal(t, "old-token", w.resumeToken)
		assert.Equal(t, 0, fr.resumeCalls)
	})
}
