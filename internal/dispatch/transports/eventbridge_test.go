package transports

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEventBridgeValidation(t *testing.T) {
	tests := []struct {
		name    string
		spec    EventBridgeSpec
		wantNil bool
	}{
		{name: "missing event_bus_name", spec: EventBridgeSpec{Source: "conduit"}, wantNil: true},
		{name: "empty spec", spec: EventBridgeSpec{}, wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := NewEventBridge(context.Background(), tt.spec)
			if tt.wantNil {
				assert.Nil(t, transport)
			} else {
				assert.NotNil(t, transport)
			}
		})
	}
}

func TestNewEventBridgeRejectsMissingBus(t *testing.T) {
	assert.Nil(t, NewEventBridge(context.Background(), EventBridgeSpec{}))
	assert.Nil(t, NewEventBridge(context.Background(), EventBridgeSpec{Source: "conduit"}))
}

// isolateAWSCredentials pins the AWS SDK credential chain to a known-empty
// state: environment variables unset (empty = unset for the chain), shared
// config/credentials pointed at empty temp files, and IMDS disabled so it
// cannot answer. This shields the test from the developer's real environment.
// t.Setenv cannot be combined with t.Parallel; the tests here are not parallel.
func isolateAWSCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")

	configFile := t.TempDir() + "/config"
	credsFile := t.TempDir() + "/credentials"
	if err := os.WriteFile(configFile, nil, 0o600); err != nil {
		t.Fatalf("write empty config file: %v", err)
	}
	if err := os.WriteFile(credsFile, nil, 0o600); err != nil {
		t.Fatalf("write empty credentials file: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsFile)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// isolateAWSRegion pins the AWS SDK region chain to a known-empty state:
// region env vars unset and the shared config pointed at an empty temp file so
// the developer's real ~/.aws/config cannot leak a region. t.Setenv cannot be
// combined with t.Parallel; the tests here are not parallel.
func isolateAWSRegion(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")

	configFile := t.TempDir() + "/config"
	if err := os.WriteFile(configFile, nil, 0o600); err != nil {
		t.Fatalf("write empty config file: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configFile)
}

func TestNewEventBridgeWithoutCredentialsFails(t *testing.T) {
	isolateAWSCredentials(t)
	isolateAWSRegion(t)
	t.Setenv("AWS_REGION", "us-east-1")
	assert.Nil(t, NewEventBridge(context.Background(), EventBridgeSpec{EventBusName: "default"}))
}

func TestNewEventBridgeWithEnvCredentials(t *testing.T) {
	isolateAWSCredentials(t)
	isolateAWSRegion(t)
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	assert.NotNil(t, NewEventBridge(context.Background(), EventBridgeSpec{EventBusName: "default"}))
}

func TestNewEventBridgeWithoutRegionFails(t *testing.T) {
	// Valid credentials so the credential probe is not what trips construction;
	// the fail-fast must come from the region check with no region in any source.
	isolateAWSRegion(t)
	isolateAWSCredentials(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	assert.Nil(t, NewEventBridge(context.Background(), EventBridgeSpec{EventBusName: "default"}))
}

func TestNewEventBridgeWithRegionFromEnv(t *testing.T) {
	// Credentials are set only so the region check is what's under test; without
	// them construction would fail at the credential probe, not the region check.
	isolateAWSRegion(t)
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	assert.NotNil(t, NewEventBridge(context.Background(), EventBridgeSpec{EventBusName: "default"}))
}

func TestBuildEventDetail(t *testing.T) {
	record := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1", "title": "hello"},
		OldImage:   nil,
		Timestamp:  mustTime("2024-01-01T00:00:00Z"),
		EventID:    "evt-1",
	}

	detail, err := buildEventDetail(record)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(detail), &parsed))
	assert.Equal(t, "movies", parsed["tableName"])
	assert.Equal(t, "INSERT", parsed["recordType"])
	assert.Equal(t, "evt-1", parsed["eventId"])
	_, ok := parsed["newImage"].(map[string]interface{})
	assert.True(t, ok, "newImage should be present as an object")
	_, hasOld := parsed["oldImage"]
	assert.False(t, hasOld, "oldImage should be omitted when nil")
	_, hasTS := parsed["timestamp"]
	assert.True(t, hasTS, "timestamp should be present")
}

func TestEventBridgeDetailOversize(t *testing.T) {
	// Build a record whose serialized detail exceeds the 256KB PutEvents limit.
	big := strings.Repeat("x", maxPutEventsEntrySize)
	record := streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1", "big": big},
		Timestamp:  mustTime("2024-01-01T00:00:00Z"),
		EventID:    "evt-big",
	}

	detail, err := buildEventDetail(record)
	require.NoError(t, err)
	assert.Greater(t, len(detail), maxPutEventsEntrySize)

	// The size check lives in the transport and rejects before any network call.
	// We assert the guard via a transport whose client is nil (never used).
	tr := &EventBridgeTransport{EventBridgeSpec: EventBridgeSpec{Source: defaultSource, EventBusName: "default"}}
	err = tr.Send(context.Background(), record)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeding the")
	assert.Contains(t, err.Error(), "evt-big")
}

func TestFirstFailedEntry(t *testing.T) {
	msg := firstFailedEntry([]types.PutEventsResultEntry{
		{EventId: strPtr("ok")},
		{ErrorCode: strPtr("AccessDeniedException"), ErrorMessage: strPtr("no access")},
	})
	assert.Contains(t, msg, "AccessDeniedException")
	assert.Contains(t, msg, "no access")

	msg = firstFailedEntry(nil)
	assert.Contains(t, msg, "without error details")
}

func TestBuildEventBridgeValidation(t *testing.T) {
	// A spec missing required fields is rejected by NewEventBridge.
	assert.Nil(t, buildEventBridge(context.Background(), "events", collections.SinkTypeEventBridge, map[string]interface{}{
		"source": "conduit",
	}))
	// A nil spec fails decode.
	assert.Nil(t, buildEventBridge(context.Background(), "events", collections.SinkTypeEventBridge, nil))
}

// fakePutEvents is a test double for the PutEventsAPI seam. It records the last
// context it received and returns a canned result or error.
type fakePutEvents struct {
	putErr     error
	out        *eventbridge.PutEventsOutput
	calls      int
	deadlineOk bool
}

func (f *fakePutEvents) PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	f.calls++
	_, f.deadlineOk = ctx.Deadline()
	return f.out, f.putErr
}

func TestEventBridgeSendHappyPath(t *testing.T) {
	fake := &fakePutEvents{out: &eventbridge.PutEventsOutput{FailedEntryCount: 0}}
	tr := &EventBridgeTransport{EventBridgeSpec: EventBridgeSpec{Source: defaultSource, EventBusName: "default"}, client: fake}

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-ok",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls)
}

func TestEventBridgeSendFailedEntry(t *testing.T) {
	fake := &fakePutEvents{out: &eventbridge.PutEventsOutput{
		FailedEntryCount: 1,
		Entries: []types.PutEventsResultEntry{
			{EventId: strPtr("ok")},
			{ErrorCode: strPtr("LimitExceeded"), ErrorMessage: strPtr("too fast")},
		},
	}}
	tr := &EventBridgeTransport{EventBridgeSpec: EventBridgeSpec{Source: defaultSource, EventBusName: "default"}, client: fake}

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-failed",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LimitExceeded")
	assert.Contains(t, err.Error(), "too fast")
}

func TestEventBridgeSendAPIError(t *testing.T) {
	fake := &fakePutEvents{putErr: errors.New("boom")}
	tr := &EventBridgeTransport{EventBridgeSpec: EventBridgeSpec{Source: defaultSource, EventBusName: "default"}, client: fake}

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-api",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "put events")
	assert.Contains(t, err.Error(), "evt-api")
	assert.Contains(t, err.Error(), "movies")
}

func TestEventBridgeSendAppliesTimeout(t *testing.T) {
	fake := &fakePutEvents{out: &eventbridge.PutEventsOutput{FailedEntryCount: 0}}
	tr := &EventBridgeTransport{EventBridgeSpec: EventBridgeSpec{Source: defaultSource, EventBusName: "default"}, client: fake}

	err := tr.Send(context.Background(), streams.StreamRecord{
		TableName:  "movies",
		RecordType: streams.InsertRecord,
		NewImage:   map[string]interface{}{"id": "1"},
		EventID:    "evt-timeout",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.calls)
	// The per-call timeout must be applied: the fake must receive a context with
	// a deadline set, proving Send derives a bounded context around PutEvents.
	assert.True(t, fake.deadlineOk, "PutEvents should receive a context with a deadline")
}

func strPtr(s string) *string { return &s }

func mustTime(s string) (t time.Time) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
