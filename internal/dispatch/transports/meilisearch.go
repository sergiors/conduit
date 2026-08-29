package transports

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/streams"
)

// MeilisearchSpec holds the type-specific configuration for a Meilisearch transport.
type MeilisearchSpec struct {
	Host      string `bson:"host" json:"host"`
	APIKey    string `bson:"api_key,omitempty" json:"api_key,omitempty"`
	IndexName string `bson:"index_name,omitempty" json:"index_name,omitempty"`
}

// taskTimeout bounds how long we wait for a Meilisearch indexing/deletion task
// to complete. Meilisearch processing is asynchronous (the API enqueues a task
// and returns immediately), so without a bound a slow index could stall the
// watch loop forever. 5s is a generous budget for a single document.
const taskTimeout = 5 * time.Second

// taskPollInterval is how often we poll the task endpoint while waiting.
const taskPollInterval = 50 * time.Millisecond

// enqueueTimeout bounds the enqueue call (the UpdateDocuments/DeleteDocument
// request). The dispatcher passes the watcher's long-lived context into Send, so
// each transport must bound its own delivery; the SDK's default http.Client has
// no overall timeout. The subsequent task wait is separately bounded by
// taskTimeout. 30s is a generous budget for a single document enqueue.
const enqueueTimeout = 30 * time.Second

// MeilisearchTransport delivers stream records to Meilisearch for full-text indexing.
type MeilisearchTransport struct {
	MeilisearchSpec

	client meilisearch.ServiceManager
}

// NewMeilisearch builds a Meilisearch transport from its spec.
func NewMeilisearch(ctx context.Context, spec MeilisearchSpec) dispatch.Transport {
	if spec.Host == "" {
		log.Printf("Meilisearch transport requires a host")
		return nil
	}

	// Build the SDK client. WithCustomClient is not required; the SDK's default
	// http.Client is used. meilisearch-go v0.36 constructs clients via
	// meilisearch.New(host, options...).
	opts := []meilisearch.Option{}
	if spec.APIKey != "" {
		opts = append(opts, meilisearch.WithAPIKey(spec.APIKey))
	}

	return &MeilisearchTransport{
		MeilisearchSpec: spec,
		client:          meilisearch.New(spec.Host, opts...),
	}
}

func (t *MeilisearchTransport) Send(ctx context.Context, record streams.StreamRecord) error {
	switch record.RecordType {
	case streams.InsertRecord, streams.ModifyRecord:
		if record.NewImage != nil {
			return t.indexDocument(ctx, record)
		}
		// A MODIFY with no post-image is effectively a removal.
		if record.OldImage != nil {
			return t.deleteDocument(ctx, record)
		}
		return fmt.Errorf("meilisearch: cannot index record %s (table %s): no new image and no old image to delete", record.EventID, record.TableName)
	case streams.RemoveRecord:
		return t.deleteDocument(ctx, record)
	default:
		return fmt.Errorf("meilisearch: unsupported record type %q for record %s (table %s)", record.RecordType, record.EventID, record.TableName)
	}
}

// indexDocument upserts a single document into the Meilisearch index. Meilisearch
// upserts by primary key automatically, so the document is sent as a single-element
// JSON array. Documents are indexed as-is, keyed by their MongoDB `_id` (passed as
// `primaryKey` in the request); nothing is copied or mutated. The watcher derives
// the record's DocumentID from the same `_id` (documentKey), so deletes and upserts
// always target the same identity. Meilisearch rejects documents lacking the primary
// key at task level, which waitForTask surfaces — no local duplicate validation.
func (t *MeilisearchTransport) indexDocument(ctx context.Context, record streams.StreamRecord) error {
	image, ok := record.NewImage.(map[string]interface{})
	if !ok {
		return fmt.Errorf("meilisearch: cannot index record %s (table %s): new image is not an object", record.EventID, record.TableName)
	}

	index := t.client.Index(t.IndexName)

	// CONSISTENCY MODEL: Meilisearch indexing is asynchronous. The UpdateDocuments
	// call only enqueues a task and returns a TaskInfo immediately. To make
	// "success" mean the document is actually committed (so the retry pipeline only
	// advances after the write is durable), we await the enqueued task. The wait is
	// bounded by taskTimeout so a slow Meilisearch cannot block the watch loop
	// forever. A failed/canceled task or a wait timeout surfaces as an error.
	enqueueCtx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()

	// The Meilisearch primary key is the MongoDB `_id`. Per MongoDB's
	// documentation, `_id` is reserved as the primary key: it always exists,
	// its value is unique within the collection, and it is immutable — exactly
	// the contract a Meilisearch primary key requires (stable identity for
	// upserts and deletes). The watcher derives StreamRecord.DocumentID from
	// the same documentKey._id, so both code paths target the same key.
	// DocumentOptions.PrimaryKey is *string, hence StringPtr.
	primaryKey := meilisearch.StringPtr("_id")
	task, err := index.UpdateDocumentsWithContext(enqueueCtx, []interface{}{image}, &meilisearch.DocumentOptions{PrimaryKey: primaryKey})
	if err != nil {
		return fmt.Errorf("meilisearch: update documents for record %s (table %s): %w", record.EventID, record.TableName, err)
	}

	return t.waitForTask(ctx, task, record)
}

// deleteDocument removes a document from the Meilisearch index by its primary key.
// The transport uses only the deterministic document ID provided by the event
// (record.DocumentID, the MongoDB `_id` stringified — hex for ObjectID). It never
// inspects the document payload for an ID. A missing DocumentID is an error: the
// event goes to retry and, failing, to the DLQ. Loud, never silent.
func (t *MeilisearchTransport) deleteDocument(ctx context.Context, record streams.StreamRecord) error {
	if record.DocumentID == "" {
		return fmt.Errorf("meilisearch: cannot delete: no deterministic document id available (record %s, table %s)", record.EventID, record.TableName)
	}

	docID := record.DocumentID
	index := t.client.Index(t.IndexName)

	enqueueCtx, cancel := context.WithTimeout(ctx, enqueueTimeout)
	defer cancel()

	task, err := index.DeleteDocumentWithContext(enqueueCtx, docID, nil)
	if err != nil {
		return fmt.Errorf("meilisearch: delete document for record %s (table %s): %w", record.EventID, record.TableName, err)
	}

	return t.waitForTask(ctx, task, record)
}

// waitForTask awaits a Meilisearch task until it completes, fails, or the bounded
// timeout elapses.
func (t *MeilisearchTransport) waitForTask(ctx context.Context, task *meilisearch.TaskInfo, record streams.StreamRecord) error {
	waitCtx, cancel := context.WithTimeout(ctx, taskTimeout)
	defer cancel()

	resolved, err := t.client.Index(t.IndexName).WaitForTaskWithContext(waitCtx, task.TaskUID, taskPollInterval)
	if err != nil {
		return fmt.Errorf("meilisearch: await task %d for record %s (table %s): %w", task.TaskUID, record.EventID, record.TableName, err)
	}

	if resolved.Status == meilisearch.TaskStatusFailed || resolved.Status == meilisearch.TaskStatusCanceled {
		msg := resolved.Error.Message
		if msg == "" {
			msg = resolved.Error.Code
		}
		if msg == "" {
			msg = "no error details provided"
		}
		return fmt.Errorf("meilisearch: task %d for record %s (table %s) ended with status %q: %s", task.TaskUID, record.EventID, record.TableName, resolved.Status, msg)
	}

	return nil
}

func (t *MeilisearchTransport) Close() error { return nil }

// buildMeilisearch decodes a raw spec and builds a Meilisearch transport.
func buildMeilisearch(ctx context.Context, collectionName string, t collections.Type, rawSpec map[string]interface{}) dispatch.Transport {
	var spec MeilisearchSpec
	if err := decodeSpec(rawSpec, &spec); err != nil {
		log.Printf("Failed to decode Meilisearch transport spec for %s: %v", collectionName, err)
		return nil
	}

	if spec.IndexName == "" {
		spec.IndexName = collectionName
	}

	return NewMeilisearch(ctx, spec)
}

func init() {
	dispatch.RegisterTransport(collections.SinkTypeMeilisearch, buildMeilisearch)
}
