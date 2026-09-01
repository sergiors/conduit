package transports

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// EventBridgeSpec holds the type-specific configuration for an EventBridge transport.
type EventBridgeSpec struct {
	EventBusName string `bson:"eventBusName" json:"eventBusName"`
	Source       string `bson:"source,omitempty" json:"source,omitempty"`
}

// defaultSource is used when the spec does not provide a Source.
const defaultSource = "conduit-mongodb"

// detailType is the EventBridge DetailType for stream records.
const detailType = "conduit-stream-record"

// maxPutEventsEntrySize is the maximum size, in bytes, of the Detail JSON for a
// single PutEvents entry (256KB per AWS documentation).
const maxPutEventsEntrySize = 256 * 1024

// putEventsTimeout bounds the PutEvents call. The dispatcher passes the watcher's
// long-lived context into Send, so each transport must bound its own delivery;
// the AWS SDK imposes no deadline beyond ctx cancellation. 30s is a generous
// budget for a single PutEvents request.
const putEventsTimeout = 30 * time.Second

// PutEventsAPI is the minimal interface EventBridgeTransport needs from the
// EventBridge client. The generated *eventbridge.Client satisfies it; a narrow
// interface keeps the transport testable without a real AWS client.
type PutEventsAPI interface {
	PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

// EventBridgeTransport delivers stream records to AWS EventBridge via PutEvents.
type EventBridgeTransport struct {
	EventBridgeSpec

	client PutEventsAPI
}

// NewEventBridge builds an EventBridge transport from its spec.
func NewEventBridge(ctx context.Context, spec EventBridgeSpec) dispatch.Transport {
	if spec.EventBusName == "" {
		log.Printf("EventBridge transport requires an eventBusName")
		return nil
	}

	if spec.Source == "" {
		spec.Source = defaultSource
	}

	// Load the default AWS configuration so the standard credential chain works
	// (environment variables, shared config/credentials files, IAM roles, etc.).
	// The region is resolved via the SDK's own default region chain (AWS_REGION,
	// shared config, or the compute environment) — there is
	// no spec region and no WithRegion option. Construction eagerly probes the
	// credential chain below so that a misconfigured deployment surfaces at
	// registration time, not at first delivery (LoadDefaultConfig alone
	// succeeds even with no credentials).
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("EventBridge transport: failed to load AWS config: %v", err)
		return nil
	}

	// Region is infrastructure configuration, never sink configuration: it must
	// come from the AWS SDK default region chain (AWS_REGION, ~/.aws/config,
	// or the compute environment). Empty means the operator did
	// not configure one anywhere, so registration fails fast with guidance.
	if cfg.Region == "" {
		log.Printf("EventBridge transport: no AWS region resolved; set AWS_REGION or configure the region in the shared AWS config / compute environment")
		return nil
	}

	// Resolve credentials from the SDK's own default chain so a deployment
	// without any usable credentials fails fast here instead of at PutEvents
	// time with an opaque signing error.
	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		log.Printf("EventBridge transport: no AWS credentials resolved: %v; provide credentials via the AWS SDK default chain (e.g. AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, shared credentials file, or an IAM role)", err)
		return nil
	}

	return &EventBridgeTransport{
		EventBridgeSpec: spec,
		client:          eventbridge.NewFromConfig(cfg),
	}
}

// Send publishes a stream record to the configured EventBridge event bus.
func (t *EventBridgeTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	detail, err := buildEventDetail(record)
	if err != nil {
		return fmt.Errorf("eventbridge: %w (record %s, table %s)", err, record.EventID, record.TableName)
	}

	// EventBridge PutEvents entries are limited to 256KB. We never truncate —
	// truncation would silently alter the event — so an oversized detail is an
	// explicit error before any network call.
	if len(detail) > maxPutEventsEntrySize {
		return fmt.Errorf("eventbridge: detail for record %s (table %s) is %d bytes, exceeding the %d byte PutEvents limit", record.EventID, record.TableName, len(detail), maxPutEventsEntrySize)
	}

	entry := types.PutEventsRequestEntry{
		EventBusName: aws.String(t.EventBusName),
		Source:       aws.String(t.Source),
		DetailType:   aws.String(detailType),
		Time:         &record.Timestamp,
		Detail:       aws.String(detail),
	}

	putCtx, cancel := context.WithTimeout(ctx, putEventsTimeout)
	defer cancel()

	resp, err := t.client.PutEvents(putCtx, &eventbridge.PutEventsInput{
		Entries: []types.PutEventsRequestEntry{entry},
	})
	if err != nil {
		return fmt.Errorf("eventbridge: put events for record %s (table %s): %w", record.EventID, record.TableName, err)
	}

	// PutEvents can fail per-entry. We send exactly one entry, so any failed
	// entry means delivery failed — do not swallow it.
	if resp.FailedEntryCount > 0 {
		msg := firstFailedEntry(resp.Entries)
		return fmt.Errorf("eventbridge: failed entry for record %s (table %s): %s", record.EventID, record.TableName, msg)
	}

	return nil
}

// buildEventDetail marshals a stream record into the Detail JSON for an
// EventBridge entry. StreamRecord has stable json tags, so it is used directly.
func buildEventDetail(record streams.StreamRecord) (string, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("marshal record: %w", err)
	}
	return string(data), nil
}

// firstFailedEntry describes the first failed entry in a PutEvents response, or
// returns a generic message if none can be inspected.
func firstFailedEntry(entries []types.PutEventsResultEntry) string {
	for _, e := range entries {
		if e.ErrorCode != nil || e.ErrorMessage != nil {
			code := ""
			message := ""
			if e.ErrorCode != nil {
				code = *e.ErrorCode
			}
			if e.ErrorMessage != nil {
				message = *e.ErrorMessage
			}
			return fmt.Sprintf("error code %q: %s", code, message)
		}
	}
	return "entry failed without error details"
}

func (t *EventBridgeTransport) Close() error { return nil }

// buildEventBridge decodes a raw spec and builds an EventBridge transport.
func buildEventBridge(ctx context.Context, collectionName string, t collections.Type, rawSpec map[string]interface{}) dispatch.Transport {
	var spec EventBridgeSpec
	if err := decodeSpec(rawSpec, &spec); err != nil {
		log.Printf("Failed to decode EventBridge transport spec for %s: %v", collectionName, err)
		return nil
	}

	return NewEventBridge(ctx, spec)
}

func init() {
	dispatch.RegisterTransport(collections.SinkTypeEventBridge, buildEventBridge)
}
