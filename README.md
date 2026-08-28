# Conduit

MongoDB Change Data Capture with a DynamoDB-style mental model.

Conduit watches MongoDB collections and forwards change events to external sinks. Configure collections, streams, and destinations through a REST API; workers apply changes at runtime without restarting.

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
- **Runtime configuration** — create, update, or remove collections, streams, and sinks without restarting workers.
- **Multiple sink types** — HTTP webhooks today; EventBridge and Meilisearch registered for future SDK integration.
- **Per-sink filtering** — filter by event type and by content of `newImage` / `oldImage`.
- **Retry with exponential backoff** — failed deliveries are retried up to 5 times.
- **Dead Letter Queue** — exhausted retries land in a per-collection DLQ.
- **Resume tokens** — per-collection resume positions stored in Redis, saved only after successful processing.
- **Automatic watcher management** — one watcher per enabled collection, synchronized dynamically with configuration.
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
- **MongoDB** — stores configuration and application data; emits change stream events.
- **Redis** — worker state: resume tokens, retry queues, DLQ, idempotency, and configuration-change notifications.
- **Worker** — consumes change streams, dispatches events to sinks, and manages retries.

---

## How it Works

MongoDB emits a change event → a per-collection watcher turns it into a `StreamRecord` → the dispatcher fans it out to the collection's sinks → failures are queued for retry with exponential backoff → exhausted retries move to the DLQ.

Resume tokens are updated only after successful processing, so events are never skipped on failure.

---

## Configuration

Configuration is applied at runtime through the REST API. Workers detect changes automatically via Redis Pub/Sub and periodic polling.

- **Collections** — the root resource. Define the MongoDB collection name, optional `partition_key`/`sort_key`, and deletion protection.
- **Streams** — opt a collection into CDC and choose whether to include `old_image`.
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

- `POST /api/collections/:name/stream` — enable streaming with `old_image`
- `DELETE /api/collections/:name/stream` — disable streaming

### TTL

- `POST /api/collections/:name/ttl` — enable TTL on a field
- `DELETE /api/collections/:name/ttl` — disable TTL

### Deletion Protection

- `POST /api/collections/:name/protection` — enable protection
- `DELETE /api/collections/:name/protection` — disable protection

### Sinks

- `GET /api/collections/:name/sinks` — list sinks
- `POST /api/collections/:name/sinks` — create a sink
- `DELETE /api/collections/:name/sinks/:id` — delete a sink

### Documents

- `GET /api/collections/:name/documents` — list documents
- `GET /api/collections/:name/documents/:id` — get a document

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

Each sink type owns its own configuration, which lives in a `spec` object in the sink payload. The shared sink model only carries common metadata (`type`, `event_types`, `filter_criteria`).

### HTTP

Sends `StreamRecord` JSON documents to a webhook endpoint via `POST`. Supports a bearer token and per-sink filtering.

```json
{
  "type": "http",
  "spec": {
    "endpoint": "https://example.com/webhook",
    "bearer_token": "secret"
  }
}
```

### AWS EventBridge

Registered sink type with a skeleton implementation. The AWS SDK integration is not yet wired in.

```json
{
  "type": "eventbridge",
  "spec": {
    "region": "us-east-1",
    "event_bus_name": "default",
    "source": "conduit"
  }
}
```

### Meilisearch

Registered sink type with a skeleton implementation. The Meilisearch client integration is not yet wired in.

```json
{
  "type": "meilisearch",
  "spec": {
    "host": "https://search.example.com",
    "api_key": "secret",
    "index_name": "users"
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
- **Configuration-driven** — workers reconcile their runtime state from `config.collections`.
- **Dynamic runtime updates** — configuration changes take effect without restarting workers.
- **Immutable configuration resources** — stream, TTL, and protection settings are changed by disabling and recreating them.
- **API as an HTTP interface** — the API only adapts HTTP requests; business rules live in the domain layer.
- **No event loss** — failed events retry; exhausted retries go to the DLQ. Resume tokens advance only on success.

---

## License

Conduit is licensed under the GNU General Public License v3.0. See [LICENSE.md](LICENSE.md) for details.
