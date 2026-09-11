package transports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"conduit/internal/collections"
	"conduit/internal/dispatch"
	"conduit/internal/streams"
)

var discardLogger = log.New(io.Discard, "", 0)

// fakeXAdder is a test double for the XAdder seam. It records the last XAddArgs
// and context it received and returns a canned result or error.
type fakeXAdder struct {
	args       *redis.XAddArgs
	ctx        context.Context
	calls      int
	deadlineOk bool
	result     *redis.StringCmd
}

func (f *fakeXAdder) XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
	f.calls++
	f.args = a
	f.ctx = ctx
	_, f.deadlineOk = ctx.Deadline()
	return f.result
}

func (f *fakeXAdder) Close() error { return nil }

func TestNewRedisValidation(t *testing.T) {
	tests := []struct {
		name    string
		spec    RedisSpec
		wantNil bool
	}{
		{name: "missing url", spec: RedisSpec{Stream: "events"}, wantNil: true},
		{name: "empty url", spec: RedisSpec{URL: "", Stream: "events"}, wantNil: true},
		{name: "missing stream", spec: RedisSpec{URL: "redis://localhost:6379/0"}, wantNil: true},
		{name: "empty stream", spec: RedisSpec{URL: "redis://localhost:6379/0", Stream: ""}, wantNil: true},
		{name: "invalid url", spec: RedisSpec{URL: "not-a-redis-url", Stream: "events"}, wantNil: true},
		{name: "valid spec", spec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, wantNil: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := NewRedis(context.Background(), tt.spec, discardLogger)
			if tt.wantNil {
				assert.Nil(t, transport)
			} else {
				assert.NotNil(t, transport)
			}
		})
	}
}

func TestRedisSendHappyPath(t *testing.T) {
	fake := &fakeXAdder{result: redis.NewStringResult("1-1", nil)}
	tr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: fake}

	record := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1", "title": "hello"},
		Timestamp:  mustTime("2024-01-01T00:00:00Z"),
		EventID:    "evt-1",
	}

	err := tr.Send(context.Background(), record)
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls)

	// Stream must be the configured one.
	assert.Equal(t, "events", fake.args.Stream)

	// Values must contain exactly the "event" field.
	values, ok := fake.args.Values.(map[string]interface{})
	require.True(t, ok, "Values should be a map")
	require.Contains(t, values, "event")

	// The "event" value must equal json.Marshal(record) exactly.
	want, err := json.Marshal(record)
	require.NoError(t, err)
	assert.Equal(t, string(want), values["event"])

	// ID empty (auto-generated "*") and MaxLen 0 (no trimming).
	assert.Equal(t, "", fake.args.ID)
	assert.Equal(t, int64(0), fake.args.MaxLen)
}

func TestRedisSendPreservesNestedImages(t *testing.T) {
	fake := &fakeXAdder{result: redis.NewStringResult("1-1", nil)}
	tr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: fake}

	record := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.ModifyRecord,
		NewImage: map[string]interface{}{
			"id":     "1",
			"nested": map[string]interface{}{"a": 1, "b": []interface{}{"x", "y"}},
		},
		OldImage: map[string]interface{}{
			"id":     "1",
			"nested": map[string]interface{}{"a": 0},
		},
		Timestamp: mustTime("2024-01-01T00:00:00Z"),
		EventID:   "evt-nested",
	}

	require.NoError(t, tr.Send(context.Background(), record))

	values := fake.args.Values.(map[string]interface{})
	eventJSON := values["event"].(string)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(eventJSON), &parsed))

	newImage, ok := parsed["newImage"].(map[string]interface{})
	require.True(t, ok, "newImage should be an object")
	nested, ok := newImage["nested"].(map[string]interface{})
	require.True(t, ok, "nested newImage should be an object")
	assert.Equal(t, float64(1), nested["a"])
	assert.Equal(t, []interface{}{"x", "y"}, nested["b"])

	oldImage, ok := parsed["oldImage"].(map[string]interface{})
	require.True(t, ok, "oldImage should be an object")
	oldNested, ok := oldImage["nested"].(map[string]interface{})
	require.True(t, ok, "nested oldImage should be an object")
	assert.Equal(t, float64(0), oldNested["a"])
}

func TestRedisSendFailurePropagates(t *testing.T) {
	fake := &fakeXAdder{result: redis.NewStringResult("", errors.New("boom"))}
	tr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: fake}

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-fail",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "xadd")
	assert.Contains(t, err.Error(), "evt-fail")
	assert.Contains(t, err.Error(), "movies")
	assert.Contains(t, err.Error(), "boom")
}

func TestRedisSendAppliesTimeout(t *testing.T) {
	fake := &fakeXAdder{result: redis.NewStringResult("1-1", nil)}
	tr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: fake}

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-timeout",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls)
	// The per-call timeout must be applied: the fake must receive a context with
	// a deadline set, proving Send derives a bounded context around XADD.
	assert.True(t, fake.deadlineOk, "XAdd should receive a context with a deadline")
}

func TestRedisSendMarshalFailure(t *testing.T) {
	fake := &fakeXAdder{result: redis.NewStringResult("1-1", nil)}
	tr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: fake}

	// A channel is not JSON-marshalable, so json.Marshal must fail before any
	// XADD call.
	record := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1", "ch": make(chan int)},
		EventID:    "evt-marshal",
	}

	err := tr.Send(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal")
	assert.Contains(t, err.Error(), "evt-marshal")
	assert.Equal(t, 0, fake.calls, "XADD must not be called when marshaling fails")
}

func TestBuildRedisValidation(t *testing.T) {
	ctx := context.Background()

	// Missing url.
	assert.Nil(t, buildRedis(ctx, "users", collections.SinkTypeRedis, map[string]interface{}{
		"stream": "events",
	}, discardLogger))
	// Empty url.
	assert.Nil(t, buildRedis(ctx, "users", collections.SinkTypeRedis, map[string]interface{}{
		"url":    "",
		"stream": "events",
	}, discardLogger))
	// Missing stream.
	assert.Nil(t, buildRedis(ctx, "users", collections.SinkTypeRedis, map[string]interface{}{
		"url": "redis://localhost:6379/0",
	}, discardLogger))
	// Empty stream.
	assert.Nil(t, buildRedis(ctx, "users", collections.SinkTypeRedis, map[string]interface{}{
		"url":    "redis://localhost:6379/0",
		"stream": "",
	}, discardLogger))
	// Nil spec fails decode.
	assert.Nil(t, buildRedis(ctx, "users", collections.SinkTypeRedis, nil, discardLogger))
}

func TestBuildRedisValid(t *testing.T) {
	tr := buildRedis(context.Background(), "users", collections.SinkTypeRedis, map[string]interface{}{
		"url":    "redis://localhost:6379/0",
		"stream": "events",
	}, discardLogger)
	require.NotNil(t, tr)

	rt, ok := tr.(*RedisTransport)
	require.True(t, ok, "built transport should be a *RedisTransport")
	// No collection-name defaulting: the stream must be the spec value, not the
	// collection name.
	assert.Equal(t, "events", rt.Stream)
	assert.Equal(t, "redis://localhost:6379/0", rt.URL)
}

func TestRedisTransportClose(t *testing.T) {
	// NewRedis builds a real *redis.Client for an unreachable address; Close
	// must not error and must be safe.
	tr := NewRedis(context.Background(), RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, discardLogger)
	require.NotNil(t, tr)
	assert.NoError(t, tr.Close())
}

// TestRedisTransportThroughDispatcher proves a redis-backed sink behaves like
// any other sink through RuntimeSink: eventTypes/filter routing and settlement
// are reused, and delivery only happens for matching events.
func TestRedisTransportThroughDispatcher(t *testing.T) {
	ctx := context.Background()
	d := dispatch.NewDispatcher()

	fake := &fakeXAdder{result: redis.NewStringResult("1-1", nil)}
	tr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: fake}

	sink := collections.Sink{
		ID:         "s1",
		Type:       collections.SinkTypeRedis,
		Spec:       map[string]interface{}{"url": "redis://localhost:6379/0", "stream": "events"},
		EventTypes: []string{"INSERT"},
	}
	d.Register("users", dispatch.NewRuntimeSink(sink, tr))

	// A matching INSERT is delivered.
	err := d.Dispatch(ctx, "users", streams.StreamRecord{
		TableName:  "users",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-insert",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls, "matching INSERT should be delivered")

	// A MODIFY excluded by eventTypes is filtered out and not delivered.
	err = d.Dispatch(ctx, "users", streams.StreamRecord{
		TableName:  "users",
		RecordType: streams.ModifyRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-modify",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls, "MODIFY excluded by eventTypes should not be delivered")

	// When XADD fails, Dispatch returns an error (settlement contract).
	failing := &fakeXAdder{result: redis.NewStringResult("", errors.New("boom"))}
	failingTr := &RedisTransport{RedisSpec: RedisSpec{URL: "redis://localhost:6379/0", Stream: "events"}, client: failing}
	d.Register("users", dispatch.NewRuntimeSink(collections.Sink{ID: "s2", Type: collections.SinkTypeRedis}, failingTr))

	err = d.Dispatch(ctx, "users", streams.StreamRecord{
		TableName:  "users",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-fail",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "xadd")
}

// TestRedisTransportIntegration exercises the transport against a live Redis
// (localhost:6379). It is skipped under -short.
func TestRedisTransportIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// NewRedis parses the URL only, so it succeeds even if Redis is down; the
	// client reconnects on demand.
	tr := NewRedis(ctx, RedisSpec{URL: "redis://localhost:6379/0", Stream: "conduit-test:" + fmt.Sprint(time.Now().UnixNano())}, discardLogger)
	require.NotNil(t, tr)
	defer tr.Close()

	rt := tr.(*RedisTransport)
	streamKey := rt.Stream

	// A raw client for verification and cleanup.
	raw := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer raw.Close()
	if err := raw.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	defer raw.Del(ctx, streamKey)

	record := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1", "nested": map[string]interface{}{"a": 1}},
		Timestamp:  time.Now(),
		EventID:    "evt-integration-1",
	}

	require.NoError(t, tr.Send(ctx, record))

	// XRANGE the stream: exactly 1 entry.
	entries, err := raw.XRange(ctx, streamKey, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Entry ID auto-generated (non-empty, format N-N).
	assert.NotEmpty(t, entries[0].ID)
	assert.Regexp(t, `^\d+-\d+$`, entries[0].ID)

	// Entry values contain "event" with the exact JSON.
	eventVal, ok := entries[0].Values["event"]
	require.True(t, ok, "entry should contain an event field")
	eventStr, ok := eventVal.(string)
	require.True(t, ok, "event field should be a string")

	want, err := json.Marshal(record)
	require.NoError(t, err)
	assert.Equal(t, string(want), eventStr)

	// Unmarshal and verify the record fields are intact.
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(eventStr), &parsed))
	assert.Equal(t, "movies", parsed["tableName"])
	assert.Equal(t, "INSERT", parsed["recordType"])
	assert.Equal(t, "evt-integration-1", parsed["eventId"])
	newImage, ok := parsed["newImage"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(1), newImage["nested"].(map[string]interface{})["a"])

	// Send a second record; XRANGE now has 2 entries (appended to the same key).
	record2 := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "2"},
		Timestamp:  time.Now(),
		EventID:    "evt-integration-2",
	}
	require.NoError(t, tr.Send(ctx, record2))

	entries, err = raw.XRange(ctx, streamKey, "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, entries, 2)
}
