# Conduit

MongoDB Change Data Capture with a DynamoDB-style mental model.

Conduit watches MongoDB collections and forwards change events to external sinks. Configure collections, streams, and destinations through a REST API; the worker applies changes at runtime without restarting.

> **⚠️ Development Status**
> This project is under active development. APIs, configuration formats, and
> architecture decisions may change without prior notice. Use with caution in
> production environments.

---

## What is Conduit?

MongoDB change streams are a solid foundation for CDC, but building a reliable pipeline around them requires handling resume tokens, retries, dead-letter queues, multiple sinks, and safe configuration updates. Conduit packages that plumbing into a small control-plane + data-plane system.

It is compatible with the DynamoDB programming model—collections, items, streams, new/old images, and TTL—while storing data in MongoDB. That makes it a pragmatic fit when you want DynamoDB-style CDC semantics on a MongoDB deployment.

### Why it exists

Instead of every team rebuilding the same change-stream infrastructure, Conduit provides an opinionated, runtime-configurable layer: opt collections into streaming, choose whether to include old images, define sinks, and protect collections from accidental deletion.

---

## Features

- **MongoDB Change Streams** — watch inserts, updates, replaces, and deletes.
- **Runtime configuration** — create, update, or remove collections, streams, and sinks without restarting the worker.
- **Multiple sink types** — HTTP webhooks today; EventBridge and Meilisearch registered for future SDK integration.
- **Per-sink filtering** — filter by event type and by content of `newImage` / `oldImage`.
- **Retry with exponential backoff** — failed deliveries are retried up to 5 times. Retries are themselves a duplicate source: an "ambiguous" failure (a timeout after the sink already processed the request) delivers the event twice.
- **Dead Letter Queue** — exhausted retries are persisted to a MongoDB DLQ (`config.dlq`), inspectable through the API.
- **Resume tokens** — per-collection resume positions stored in Redis, saved only after successful processing.
- **At-least-once delivery** — events are never lost, but duplicates can occur; downstream consumers must be idempotent (see [Delivery Semantics](#delivery-semantics)).
- **Automatic watcher management** — one watcher per enabled collection, synchronized dynamically with configuration (worker sync interval: 30s, plus immediate reaction to configuration changes via pub/sub).
- **Deletion protection** — enabled by default; collections cannot be deleted until protection is explicitly disabled.
- **TTL support** — configure a TTL attribute and Conduit creates the corresponding MongoDB TTL index.
- **Read-only document API** — inspect collection documents through the REST API.

---

## Architecture

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│     API      │────▶│   MongoDB    │◀────│    Worker    │
│   Control    │     │  Storage +   │     │  Data Plane  │
└──────────────┘     │ Change Stream│     └──────────────┘
       │             └──────────────┘            │
       │                                         │
       │         Redis (state + notifications)   │
       └─────────────────────────────────────────┘
```

- **API** — REST control plane for configuration.
- **MongoDB** — stores configuration, application data, and the dead-letter queue; emits change stream events.
- **Redis** — worker state: resume tokens, retry queues, idempotency, and configuration-change notifications.
- **Worker** — consumes change streams, dispatches events to sinks, and manages retries.

### Current worker scaling constraint

Conduit currently supports **a single active worker per deployment**. Running multiple active workers against the same collections is **not supported**.

The active worker owns Change Stream processing and resume-token progression for every enabled collection. Starting multiple worker processes against the same MongoDB/Redis deployment can create multiple readers for the same collections and competing writers for the same resume-token and retry-queue state. This is a limitation of the current architecture, not a permanent design decision; proper multi-worker coordination may be introduced in the future.

Concurrency still exists **inside** the current worker: the dispatcher delivers each event to that collection's sinks in parallel through bounded per-sink queues and worker pools. This improves sink fan-out throughput without introducing multiple Change Stream owners.

---

## How it Works

MongoDB emits a change event → a per-collection watcher turns it into a `StreamRecord` → the dispatcher fans it out to the collection's sinks **in parallel** (each sink has its own bounded queue and worker pool) → failures are queued for retry with exponential backoff → exhausted retries are persisted to the MongoDB DLQ.

The parallel fan-out increases throughput and isolates slow sinks from fast ones for each event, while keeping settlement synchronous: an event is acknowledged only after every matching sink has accepted it, preserving the single watcher and single resume-token owner. Full sink queues apply bounded backpressure rather than dropping events.

Resume tokens are updated only after successful processing, so events are never skipped on failure. Delivery is **at-least-once**, not exactly-once: see [Delivery Semantics](#delivery-semantics).

---

## Delivery Semantics

Conduit guarantees **at-least-once** delivery, **not** exactly-once. Every change event is delivered to the sinks at least once, but a duplicate delivery can occur in the following situations:

- **MarkProcessed / resume-token write fails after a successful sink dispatch.** The event is dispatched, but if the "processed" idempotency key or the resume token is not durably written (e.g. Redis is unavailable, or the process crashes between dispatch and the bookkeeping write), the change stream replays the event on the next session and it is delivered again.
- **Redis is unavailable.** The idempotency check (`IsProcessed`) and the processed-key write (`MarkProcessed`) both depend on Redis. If Redis is down, Conduit deliberately continues processing rather than drop events — so the same event can be delivered more than once.
- **Crashes and restarts.** A crash after dispatch but before the resume token is persisted causes the event to be replayed.
- **Ambiguous failures on the retry path.** A delivery error does not always mean the sink did not receive the event: a request that times out after the sink processed it (response lost), or a connection reset after a successful write, looks identical to a failure. The event goes to the retry queue and is delivered again — the sink may receive the same event twice even though Conduit behaved correctly.
- **Downtime longer than the 24h idempotency TTL.** The idempotency key expires after 24h. If a change stream replays an event whose key has already expired, it is delivered again.

Conduit's idempotency is therefore **best-effort and bounded by the Redis processed-key TTL**. Downstream consumers **must be idempotent**: they should deduplicate using the deterministic `eventID` carried on every `StreamRecord`. The `eventID` is derived from the MongoDB change event (resume token, or `clusterTime` + `documentKey` as fallback), so the same change always produces the same ID across restarts.

---

## Configuration

Configuration is applied at runtime through the REST API. The active worker detects changes automatically via Redis Pub/Sub and periodic polling.

- **Collections** — the root resource. Define the MongoDB collection name, optional `partitionKey`/`sortKey`, and deletion protection.
- **Streams** — opt a collection into CDC and choose whether to include `oldImage`.
- **Sinks** — define where events go. Multiple sinks per collection, each with event-type and image filtering.
- **TTL** — specify a document field for MongoDB TTL indexing.
- **Deletion protection** — prevent accidental collection deletion.

---

## REST API

### Collections

- `GET /api/collections` — list collections
- `POST /api/collections` — create a collection
- `GET /api/collections/:name` — get a collection
- `DELETE /api/collections/:name` — delete a collection (if unprotected)

### Streams

- `POST /api/collections/:name/stream` — enable streaming with `oldImage`
- `DELETE /api/collections/:name/stream` — disable streaming

When a stream is enabled, Conduit records a start checkpoint. The first watcher run opens its change stream from that checkpoint, so every event written after enablement is streamed — there is no gap between enabling the stream and the worker picking it up (previously the first stream started at "now" and silently skipped that window).

### TTL

- `POST /api/collections/:name/ttl` — enable TTL on a field
- `DELETE /api/collections/:name/ttl` — disable TTL

### Deletion Protection

- `POST /api/collections/:name/protection` — enable protection
- `DELETE /api/collections/:name/protection` — disable protection

### Sinks

- `GET /api/collections/:name/sinks` — list sinks
- `POST /api/collections/:name/sinks` — create a sink
- `PATCH /api/collections/:name/sinks/:id` — update a sink's mutable fields (`filter`, `eventTypes`); `type`/`spec` are immutable and return `400 sink_identity_immutable`
- `DELETE /api/collections/:name/sinks/:id` — delete a sink

### Documents

- `GET /api/collections/:name/documents` — list documents
- `GET /api/collections/:name/documents/:id` — get a document

The list endpoint is paginated server-side and never returns an unbounded
result set:

- `limit` — maximum documents per page; default `100`, capped at `1000`.
  Zero, negative, and non-integer values are rejected with `400 invalid_request`.
- `skip` — number of documents to skip before the page; default `0`.
  Negative and non-integer values are rejected with `400 invalid_request`.

Results are sorted by `_id` ascending, so pages are deterministic. Example:

```bash
curl -H "Authorization: Bearer $API_KEY" \
  "http://localhost:8080/api/collections/users/documents?limit=50&skip=100"
```

### Dead Letter Queue

- `GET /api/collections/:name/dlq` — list dead-letter entries for a collection
- `GET /api/collections/:name/dlq/:id` — get a single dead-letter entry

The DLQ is the terminal destination for events that could not be delivered
after the maximum number of retries. Entries are persisted to the MongoDB
collection `config.dlq` (not Redis) and are read-only through the API;

The list endpoint is paginated server-side and never returns an unbounded
result set:

- `limit` — maximum entries per page; default `100`, capped at `1000`.
  Zero, negative, and non-integer values are rejected with `400 invalid_request`.
- `skip` — number of entries to skip before the page; default `0`.
  Negative and non-integer values are rejected with `400 invalid_request`.

Results are sorted by `failedAt` descending, so pages are deterministic. Both
endpoints validate the collection through `config.collections` first: an
unknown or unmanaged collection returns `404 collection_not_found`, and a
single entry is only returned if it belongs to the requested collection
(otherwise `404 dlq_entry_not_found`). Example:

```bash
curl -H "Authorization: Bearer $API_KEY" \
  "http://localhost:8080/api/collections/users/dlq?limit=50&skip=100"
```

### Health

- `GET /health` — health check

### Authentication

All `/api/*` endpoints require a bearer token:

```bash
curl -H "Authorization: Bearer $API_KEY" http://localhost:8080/api/collections
```

- Send the token as `Authorization: Bearer $API_KEY` on every `/api/*` request.
- `/health` is exempt and requires no token.
- `API_KEY` is **required**: the API refuses to start without it.

---

## Supported Sink Types

Each sink type owns its own configuration, which lives in a `spec` object in the sink payload. The shared sink model only carries common metadata (`type`, `eventTypes`, `filter`).

### Filter Criteria Semantics

`filter` is declarative: a sink **without** a `filter` receives **every event**; with a `filter`, an event is delivered only when **every declared criterion matches**.

- A filter block you don't declare is ignored.
- A declared filter block (e.g. `oldImage`) whose image is **absent** evaluates to `false` — so an `oldImage` filter never matches `INSERT` events or collections streaming with `oldImage=false`, and a `newImage` filter never matches `REMOVE` events. This is intentional: a content predicate needs content to match.
- Sink filters are independent of the collection's current configuration. Sink creation does **not** reject an `oldImage` filter on a collection currently streaming with `oldImage=false` — if you later re-enable the stream with `oldImage=true`, the existing sink simply starts matching. Until then, unmatched events are silently (by design) not delivered to that sink.
- The filter is a **flat, AND-only** predicate: an event is delivered only when _every_ declared criterion matches. Complex boolean expressions (OR, etc.) are expressed by creating **multiple sinks** on the same destination.

The full operator reference is in [`docs/filter.md`](docs/filter.md).

`filter` and `eventTypes` changes apply **live**: a PATCH is picked up on the next config refresh and swapped atomically into the running sink without recreating its transport or interrupting delivery. Changing `type`/`spec` (where events go) is immutable and requires creating a new sink.

### HTTP

Sends `StreamRecord` JSON documents to a webhook endpoint via `POST`. Supports a bearer token and per-sink filtering.

```json
{
  "type": "http",
  "spec": {
    "endpoint": "https://example.com/webhook",
    "bearerToken": "secret"
  }
}
```

Delivery behavior:

- **Redirects are rejected, never followed.** The configured `endpoint` is the only delivery target. If the endpoint responds with a 3xx redirect, the delivery fails with an error (naming the redirect target) and the event flows into the normal retry pipeline — it is never silently delivered elsewhere.
- **Response bodies are drained (bounded) after a successful delivery.** A small prefix of a 2xx response body is read and discarded so the underlying HTTP connection can be reused (keep-alive) for subsequent deliveries. The body is never inspected and never read into memory; a body larger than the drain budget is ignored, not an error.
- Any non-2xx response (including a rejected redirect) is a delivery failure: the event is retried, and exhausted retries land in the DLQ.

### AWS EventBridge

Fully implemented sink that publishes `StreamRecord` documents to an AWS
EventBridge event bus via `PutEvents`. The spec contains ONLY EventBridge
routing config (`eventBusName`, and an optional `source`) — AWS credentials
and the region are NEVER part of the sink spec.

```json
{
  "type": "eventbridge",
  "spec": {
    "eventBusName": "default",
    "source": "conduit"
  }
}
```

The AWS region is resolved via the AWS SDK default region chain — `AWS_REGION`,
the shared `~/.aws/config` file, or the compute
environment (ECS task role, EC2 instance profile, EKS IRSA) — and is
infrastructure configuration, never part of the Conduit sink spec. For example:

```bash
export AWS_REGION=us-east-1
```

Credentials must be available to the app process via the AWS SDK default
credential chain: environment variables, the shared `~/.aws/credentials` file,
or an IAM role (EC2 instance profile, ECS task role, or IRSA). For example:

```bash
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...   # optional, for temporary credentials
```

Construction fails fast if no region resolves or if no credentials resolve — the
sink is skipped with a log line at registration time rather than failing on
first delivery.

### Meilisearch

Delivers stream records to Meilisearch for full-text indexing. Documents are
upserted into the configured `indexName` (keyed by the change event's
document id), and `REMOVE` events delete the document from the index.

Durability: Meilisearch processes writes asynchronously — the enqueue call
returns a task. This transport **waits for the task to complete** (bounded by
a 30s enqueue timeout and a 5s task-wait timeout per document) before
reporting success, so a delivered event is actually indexed; a failed or
timed-out delivery is retried by the pipeline's retry queue.

```json
{
  "type": "meilisearch",
  "spec": {
    "host": "https://search.example.com",
    "apiKey": "secret",
    "indexName": "users"
  }
}
```

New sinks can be added by implementing the `Sink` interface and registering a builder in `internal/dispatch/sinks`.

---

## Getting Started

### Prerequisites

- Go 1.25+
- MongoDB 4.0+ running as a replica set
- Redis 6.0+

### Environment

```bash
MONGODB_URI=mongodb://localhost:27017/?replicaSet=rs0
MONGODB_DATABASE=conduit
REDIS_URI=redis://localhost:6379
PORT=8080
API_KEY=your-secret-key
# Optional: bounded by a 30s default; applies to the worker's graceful shutdown.
# SHUTDOWN_TIMEOUT=45s
# Optional: only needed for the EventBridge sink. AWS_ACCESS_KEY_ID,
# AWS_SECRET_ACCESS_KEY, and AWS_REGION (plus optional AWS_SESSION_TOKEN) are
# resolved via the AWS SDK default credential chain.
```

### Run with Docker Compose

```bash
docker compose up -d --build
```

The API is available at http://localhost:8080.

### Run Locally

Start dependencies:

```bash
docker compose up -d mongo redis
```

Then run the API and worker:

```bash
make run-api
make run-worker
```

### Build

```bash
make build-all
```

Binaries are written to `./bin/`.

---

## Design Principles

- **Event-driven** — the data plane reacts to MongoDB change streams.
- **Configuration-driven** — the active worker reconciles its runtime state from `config.collections`.
- **Dynamic runtime updates** — configuration changes take effect without restarting the worker.
- **Immutable configuration resources** — stream, TTL, and protection settings are changed by disabling and recreating them.
- **API as an HTTP interface** — the API only adapts HTTP requests; business rules live in the domain layer.
- **No event loss** — failed events retry; exhausted retries are persisted to the MongoDB DLQ. Resume tokens advance only on success. Delivery is at-least-once, not exactly-once: duplicates can occur, so downstream consumers must be idempotent using `eventID`.

---

## License

Conduit is licensed under the GNU General Public License v3.0. See [LICENSE.md](LICENSE.md) for details.
