# Conduit Architecture

> Definitive architectural reference for the Conduit MongoDB CDC system. This document describes the project from first principles, derived from the current codebase. When the README and the code disagree, the code is the source of truth.

---

# Overview

## What is Conduit?

Conduit is a control-plane + data-plane system written in Go that turns a MongoDB deployment into a CDC (Change Data Capture) source. It lets operators define which MongoDB collections should be watched, how the resulting events should be shaped, and where those events should be delivered. It then runs a worker that opens MongoDB change streams, transforms the raw MongoDB events into a stable record format, and dispatches them to pluggable sinks.

## What Problem Does it Solve?

MongoDB change streams are powerful but low-level. Building a reliable CDC pipeline requires solving several problems repeatedly:

- Remembering the resume position per collection so restarts do not lose or duplicate events.
- Deciding whether to include the pre-image (old state) of a document.
- Delivering events to multiple downstream systems (HTTP webhooks, search indexes, event buses) with per-destination filtering.
- Retrying failed deliveries with backoff and eventually isolating poison messages in a dead-letter queue.
- Preventing accidental data loss when a collection or its configuration is removed.

Conduit centralizes these concerns behind a small REST API and a stateless worker. Operators configure collections, streams, sinks, TTL, and deletion protection through the API; the worker realizes that configuration against MongoDB and Redis.

## Why Does it Exist?

The project exists to provide DynamoDB-aligned CDC semantics on top of MongoDB. MongoDB is the storage engine, but the conceptual model—partition key, sort key, table, item, stream, new image, old image, TTL—is borrowed from DynamoDB. This makes Conduit useful for teams that want DynamoDB-style change streams but prefer (or already have) MongoDB as the backing store.

## High-Level Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   API Server    │────▶│   MongoDB       │◀────│    Worker       │
│   (Control)     │     │  (Storage +     │     │    (Data)       │
│   Port: 8080    │     │   Change Stream)│     │                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
        │                                              │
        │ Config change                                │ State
        │ notifications                                │
        ▼                                              ▼
┌─────────────────┐                           ┌─────────────────┐
│   Redis         │                           │   Redis         │
│  Pub/Sub        │                           │  State Store    │
│  cdc:config-    │                           │  resume tokens  │
│  change         │                           │  retry queues   │
└─────────────────┘                           │  DLQ            │
                                              │  idempotency    │
                                              └─────────────────┘
```

There are two runtime processes:

- **API**: Exposes REST endpoints, writes configuration to MongoDB, publishes change notifications to Redis, and provides read-only access to documents.
- **Worker**: Watches MongoDB change streams, dispatches events to sinks, manages resume tokens, retries, and DLQ. It is stateless except for the state it keeps in Redis.

---

# Core Concepts

## Collection

A _collection_ is the unit of CDC configuration. It maps 1:1 to a MongoDB collection but carries additional metadata that tells Conduit how to treat the documents inside it.

Collections are stored in the MongoDB collection `config.collections`. Each document contains the collection name, optional key schema, stream settings, TTL field, and deletion protection flag.

Collections can operate in two modes:

- **DynamoDB-compatible mode**: the operator defines a `partition_key` and an optional `sort_key`. Conduit creates a unique composite index on those fields and treats them as the logical primary key for CDC records.
- **MongoDB-native mode**: no key schema is defined. Documents are identified purely by MongoDB `_id`, and no DynamoDB-style key index is created.

## Collection Manager

_Collection Manager_ is the domain service (`internal/collections.Manager`) that owns every configuration mutation and the physical MongoDB infrastructure behind it. It is the only place where the invariants around collections, streams, sinks, TTL, and deletion protection are enforced. The API layer delegates all business decisions to it.

## Document

A _document_ is a record inside a MongoDB collection. Conduit does not write documents on behalf of callers through the API; it only reads them via `GET /api/collections/{name}/documents`. The worker, however, observes every insert, update, replace, and delete performed by application code that talks directly to MongoDB.

## Stream

A _stream_ is the CDC subscription for a collection. Streams are opt-in. A collection has a stream only when `stream_enabled` is `true`. Enabling a stream also decides whether the worker requests the pre-image (`old_image`) of each change from MongoDB.

Because `old_image` affects how the MongoDB change stream is opened, stream configuration is immutable once enabled. To change it, the operator must disable the stream and then re-enable it.

## Sink

A _sink_ is a destination for CDC events. Each sink belongs to exactly one collection and can filter by event type (`INSERT`, `MODIFY`, `REMOVE`) and by content of the `new_image` / `old_image` using the filter DSL: `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `contains`, `starts_with`, `ends_with`, `exists`, `in`, and `not_in`. See [`docs/filter.md`](docs/filter.md) for the full DSL reference.

The shared `Sink` model carries only common metadata: `type`, an opaque `spec` payload, `event_types`, and `filter`. Type-specific settings (endpoint, region, host, etc.) live inside `spec` and are owned by the individual sink implementation. This keeps the shared model stable as new sink types are added.

Sinks are persisted separately from collections in `config.sinks` so that a collection can have many sinks without bloating the collection document. The worker loads sinks when it starts or refreshes a watcher.

### Filter Semantics

`filter` is purely **declarative** — a set of conditions an event must satisfy to be delivered to that sink. Evaluation (`internal/collections.Filter`, applied in `dispatch.RuntimeSink.Send`; full reference in [`docs/filter.md`](docs/filter.md)):

- **No filter block declared** (`old_image` or `new_image` absent from `filter`): that image is not inspected at all — the block is ignored.
- **Empty filter block declared** (`"new_image": {}`): matches every event that has that image.
- **Every declared criterion must match** (AND across fields and within a field's conditions). An event is delivered only if *all* declared criteria match.
- **A declared filter block whose corresponding image is absent evaluates to `false`.** A `REMOVE` event has no `new_image`; an image the collection does not record (`old_image=false`) is always absent. This is the intended semantics: "match on the content of `old_image`" logically requires an `old_image` to exist — an absent image cannot satisfy a content predicate, the same way EventBridge patterns do not match missing fields.
- **Flat, AND-only predicates.** Filters are flat AND-only predicates per image block: every declared criterion must match. Recursive logical groups (`and` / `or`) were implemented and subsequently **removed**; boolean composition is intentionally delegated to multiple sinks — a design choice of simplicity over expression power.

Sink filters are **declarative and intentionally decoupled from the collection's current configuration**: creation does not reject an `old_image` filter when the collection's stream currently has `old_image=false`, nor `new_image` criteria that cannot match `REMOVE` events. Configuration is immutable per stream cycle, but it *can* change over the sink's lifetime (disable stream → re-enable with `old_image=true`), and a sink that already encodes its pre-image requirements is then correct without modification. Coupling sink definitions to the collection's current mode would turn a transient configuration state into a permanent restriction and break that forward compatibility. The consequence is deliberate: when the corresponding image is absent, every declared criterion fails and the event is silently not delivered — if an image is essential to the filter, also subscribe to the event types that carry it (`event_types`, plus `{"old_image": {"exists": true}}`-style predicates where appropriate) and/or enable `old_image` on the collection.

## Dispatcher

The _dispatcher_ (`internal/dispatch.Dispatcher`) is the event router inside the worker. It maintains a registry of `collection → []Sink`. When a change stream record arrives, the dispatcher calls `Send` on every sink registered for that collection. It tolerates individual sink failures: one failing sink does not prevent others from receiving the event, but the dispatcher returns the last error so the caller can decide to retry.

## Watcher

A _watcher_ (`internal/watcher.Watcher`) is a per-collection goroutine that opens a MongoDB change stream, parses each change event into a `StreamRecord`, and passes it to a handler. Each watcher manages its own resume token in Redis and restarts automatically on transient errors.

## Retry

When the dispatcher fails to deliver an event to every sink, the watcher puts the event into a per-collection Redis sorted set (`cdc:retry:{collectionName}`). A dedicated _retry processor_ polls the queues and re-attempts delivery with exponential backoff. After the configured maximum number of retries, the event is moved to the DLQ.

## DLQ

The _dead-letter queue_ (`cdc:dlq:{collectionName}`) is a Redis list that holds events that could not be delivered after the maximum number of retry attempts. It is a manual inspection and recovery point; Conduit does not currently replay DLQ events automatically.

## Resume Token

A _resume token_ is the opaque MongoDB change stream checkpoint for a collection. It is stored in Redis under `cdc:resume:{collectionName}`. The worker updates the token only after successfully processing a change. If the token becomes invalid (for example because the collection was recreated), the worker deletes it and restarts from the latest position rather than crash.

## Redis

Redis is the worker's external memory. It stores resume tokens, retry queues, the DLQ, idempotency keys, and the Pub/Sub channel used to notify the worker of configuration changes. Redis is not the event bus for the actual CDC payloads; it is only state.

## MongoDB

MongoDB serves two roles:

1. **Storage engine**: it holds the application collections, the `config.collections` settings, and the `config.sinks` configurations.
2. **CDC source**: it emits change stream events that the worker consumes.

MongoDB must run as a replica set for change streams to work. The application never creates or modifies the replica set: topology is managed externally (operators, administrators, or deployment tooling). On startup the application only waits for readiness — a writable PRIMARY that the client can reach — before serving traffic or starting watchers.

## How They Relate

```
Collection (config.collections)
    │
    ├─ 0..* Sink (config.sinks)
    │
    └─ stream_enabled = true ──▶ Watcher ──▶ Change Stream ──▶ Dispatcher ──▶ Sinks
                                                          │
                                                          ▼
                                                  Retry Queue (Redis)
                                                          │
                                                          ▼
                                                  DLQ (Redis)
```

A collection is the root. Sinks hang off the collection. When streaming is enabled, a watcher is created. The watcher feeds the dispatcher, which fans out to sinks. Failures go to the retry queue, and permanent failures go to the DLQ.

---

# Architecture

## API

### Responsibility

The API layer is the control plane. It accepts HTTP requests, validates their shape, invokes the domain packages, and returns canonical JSON responses. It contains no business rules; all invariants live in `internal/collections`. All `/api/*` routes are protected by bearer-token auth middleware; `/health` is exempt.

### Public API

- `api.New(deps)` creates a server.
- `server.Router()` returns the configured Gin engine.

### Dependencies

- `collections.Manager` for configuration CRUD and physical collection infrastructure.
- `mongo.Client` for document reads.
- `redis.Client` to publish configuration change notifications.

### What it Must Never Do

- Make decisions about streams, TTL, deletion protection, or sink validity. Those belong to `collections.Manager`.
- Leak raw infrastructure errors as HTTP responses. It must translate all errors through `responseFor`.
- Modify request payloads beyond binding.

### Why it Exists

The API exists to give operators a stable, versioned HTTP surface while keeping the domain logic decoupled from HTTP concerns. This separation makes the domain testable without starting an HTTP server and makes the API replaceable in the future.

---

## Collections Manager

### Responsibility

`collections.Manager` is the heart of the control plane. It manages the `config.collections` and `config.sinks` MongoDB collections, enforces all domain invariants, and owns the physical MongoDB infrastructure (collection creation/drop, key/TTL/stream-capability indexes, and deletion state-purge fan-out).

### Public API

- `NewManager(client, database)` constructs the store.
- `Create(ctx, &collection)` creates a collection and its physical MongoDB collection.
- `Get`, `List`, `ListStreamEnabled`, `Delete` for collection lifecycle.
- `EnableStream` / `DisableStream` for stream lifecycle.
- `SetTTL` / `DisableTTL` for TTL lifecycle.
- `EnableDeletionProtection` / `DisableDeletionProtection` for protection lifecycle.
- `CreateSink` / `GetSinks` / `DeleteSink` for sink lifecycle.

### Dependencies

- A `*mongo.Client` and database name.

### What it Must Never Do

- Dispatch events, manage watchers, or talk to Redis. It owns configuration only.
- Allow mutating stream, TTL, or deletion protection flags without explicit disable operations.
- Allow deleting a protected collection.
- Allow creating a sink on a collection that does not have streaming enabled.

### Why it Exists

Centralizing configuration mutations in one package guarantees that invariants are enforced consistently regardless of whether the caller is the REST API, a CLI, or a future gRPC service.

---

## Documents

### Responsibility

`collections.Document` provides read-only access to the documents inside a collection. It supports listing all documents and fetching a document by `_id` (either an ObjectID or a string).

### Public API

- `NewDocument(client, database, collection)` constructs a reader.
- `List(ctx)` returns all documents.
- `Get(ctx, id)` returns a single document.

### Dependencies

- A `*mongo.Client`.

### What it Must Never Do

- Write, update, or delete documents. Conduit observes writes made by external applications; it does not act as a document store client.

### Why it Exists

The document API exists for observability and debugging. It lets operators inspect the data that the worker is watching without requiring a separate MongoDB client.

---

## Watcher

### Responsibility

`watcher.Watcher` consumes the MongoDB change stream for a single collection. It parses MongoDB change events into DynamoDB-style `StreamRecord` values and hands them to the manager's handler.

### Public API

- `NewWatcher(...)` constructs a watcher with collection-specific key fields and resume token.
- `Start(ctx, handler)` begins the watch loop in a new goroutine.
- `Stop(ctx)` cancels the context and waits for the goroutine to finish.
- `OldImage()` returns whether the watcher is configured for pre-images.

### Dependencies

- A `*mongo.Client`.
- A `*redis.Client` for resume token persistence.

### What it Must Never Do

- Manage other watchers or decide whether it should exist. That is the manager's job.
- Retry or dispatch events directly. It reports events to the handler.
- Skip events. It advances the resume token only after processing succeeds.

### Why it Exists

Isolating the watch loop to one collection makes failure domains small. If one collection is dropped or its change stream is invalidated, only its watcher restarts; the rest of the worker keeps running.

---

## Dispatcher

### Responsibility

`dispatch.Dispatcher` routes a `StreamRecord` to every sink registered for the event's collection. It is a thin, concurrent-safe fan-out layer.

### Public API

- `NewDispatcher()` creates a dispatcher.
- `Register(collection, sink)` adds a sink for a collection.
- `Remove(collection, name)` removes and closes a single sink.
- `Clear(collection)` removes all sinks for a collection.
- `Dispatch(ctx, collection, record)` sends the record to all registered sinks.
- `Close()` closes every sink.

### Dependencies

- `Sink` implementations registered via `dispatch.RegisterSink`.

### What it Must Never Do

- Decide whether an event should be retried. It only reports whether any sink failed.
- Block forever on a single sink. Individual sink implementations own timeouts.

### Why it Exists

Fan-out is a separate concern from event production and from sink implementation. The dispatcher lets the system add, remove, and update sinks without touching the watcher or retry code.

---

## Streams

### Responsibility

`streams.StreamRecord` is the canonical event shape used throughout the data plane. It normalizes MongoDB operation types (`insert`, `update`, `replace`, `delete`) into `INSERT`, `MODIFY`, `REMOVE` and carries `tableName`, `newImage`, `oldImage`, and `timestamp`.

### Public API

- `StreamRecord` struct.
- `ParseStreamRecord(data)` deserializes a JSON record from Redis back into the struct.

### Dependencies

None.

### What it Must Never Do

- Contain MongoDB-specific types or raw resume tokens. Downstream sinks receive a clean, stable shape.

### Why it Exists

A stable intermediate format decouples event producers (MongoDB change streams) from event consumers (sinks). New sink types can be added without learning MongoDB driver internals.

---

## Retry Processor

### Responsibility

`retry.Processor` polls the per-collection retry queues in Redis and re-attempts delivery via the dispatcher. It uses exponential backoff capped at a maximum delay and moves exhausted events to the DLQ.

### Public API

- `NewProcessor(redisClient, dispatcher, config)` constructs the processor.
- `Start(ctx)` begins the polling loop.
- `RegisterCollection(name)` / `UnregisterCollection(name)` tell the processor which collections to scan.
- `ProcessCollectionQueue(ctx, name)` manually drains one collection's queue.

### Dependencies

- `redis.Client` for queue operations.
- `dispatch.Dispatcher` for redelivery.

### What it Must Never Do

- Lose an event. A failed retry is re-queued; an exhausted retry is moved to the DLQ.
- Block the watch loop. Retry is asynchronous.

### Why it Exists

Retry is separated from the watch loop so that a slow or failing downstream cannot stall the change stream. MongoDB change streams apply backpressure if events are not consumed quickly; moving retry work offline keeps the watcher responsive.

---

## Redis

### Responsibility

`redis.Client` wraps the Redis driver and exposes CDC-specific operations with stable key naming. It hides key construction, serialization, and Pub/Sub details from the rest of the worker.

### Public API

- `NewClient(ctx, config)` connects using URI or address/password.
- Resume token: `GetResumeToken`, `SetResumeToken`, `DeleteResumeToken`.
- Idempotency: `IsProcessed`, `MarkProcessed`.
- Retry / DLQ: `EnqueueRetry`, `DequeueRetry`, `RemoveRetryEvent`, `SendToDLQ`, length helpers.
- Notifications: `PublishConfigChange`, `SubscribeConfigChanges`.

### Dependencies

- Redis server reachable via URI or address.

### What it Must Never Do

- Store application business data. Redis is only for worker state and notifications.
- Assume a hard-coded key prefix. It uses the configured prefix (`cdc:` by default).

### Why it Exists

Isolating Redis behind a domain-specific client prevents key-format errors from spreading across packages and makes it easy to change the storage backend later.

---

## Mongo

### Responsibility

`mongo.Client` wraps the MongoDB driver. It connects, waits for MongoDB readiness, and exposes database/collection accessors. It never manages replica-set topology.

### Public API

- `NewClient(ctx, config)` connects and blocks until MongoDB is ready: the `hello` command reports a writable PRIMARY (`ok: 1`, `isWritablePrimary: true`) and the replica-set-aware client can reach it (Ping). No topology commands are ever issued.
- `Database()`, `Collection(name)` accessors.

### Dependencies

- MongoDB server.

### What it Must Never Do

- Enforce collection configuration invariants. That is `collections.Manager`.
- Create, initiate, or reconfigure replica sets. Topology is managed externally; the client is strictly read-only with respect to topology and only waits for readiness.

### Why it Exists

MongoDB connectivity and readiness gating are infrastructure concerns. Wrapping them keeps the rest of the codebase free of driver boilerplate and ensures watchers never start against a node that cannot accept writes (e.g., during elections after a restart).

---

# Request Flow

## Creating a Collection: `POST /api/collections`

1. **HTTP binding**: `bindStrictJSON` parses only `collection_name`, `partition_key`, and `sort_key`. Unknown fields are rejected.
2. **API handler** constructs a `collections.Collection` and calls `Manager.Create`.
3. **Domain validation** inside `Manager.Create`:
   - `collection_name` must be non-empty.
   - If `sort_key` is set, `partition_key` must also be set.
   - `partition_key` and `sort_key` cannot be the same.
   - `DeletionProtection` is forced to `true`.
   - `CreatedAt` and `UpdatedAt` are set.
4. **Physical collection creation**:
   - If the MongoDB collection does not exist, Conduit creates it.
   - A placeholder document is briefly inserted and removed. Empty collections can cause change-stream issues, so this guarantees the collection is materialized.
   - A unique composite index on the configured key fields is created (or ensured) when a key schema is defined.
5. **Configuration persistence**: the collection document is inserted into `config.collections`, and the generated `_id` is returned to the caller.
6. **Notification**: the API publishes the collection name to the Redis channel `cdc:config-change`.
7. **Worker reaction** (best-effort, via Pub/Sub): the watcher manager sees the notification, fetches the collection, and—because `stream_enabled` is still `false`—takes no watcher action.

The API returns `201 Created` with the collection body.

## Enabling a Stream: `POST /api/collections/{name}/stream`

1. **HTTP binding**: the body must contain `old_image` (bool, required).
2. **API handler** calls `Manager.EnableStream(ctx, name, oldImage)`.
3. **Domain enforcement**:
   - The update is conditional: it only succeeds when `stream_enabled` is not already `true`.
   - If the update matches no document, Conduit checks whether the collection exists. If it does, `ErrStreamAlreadyExists` is returned; otherwise `ErrCollectionNotFound`.
   - Stream configuration is therefore immutable while enabled.
4. **Physical MongoDB configuration**: when `old_image` is `true`, `changeStreamPreAndPostImages` is ensured on the collection via `collMod` (idempotent; collections created through Conduit already have it). A failure aborts the enablement and rolls the recorded stream back: enabling a stream with `old_image` on a deployment that cannot produce pre-images would silently drop every pre-image at the source.
5. **Configuration persistence**: `stream_enabled` and `old_image` are updated in `config.collections`.
6. **Notification**: the API publishes the collection name to `cdc:config-change`.
7. **Worker reaction**:
   - The watcher manager receives the Pub/Sub message (or discovers the change on the next poll).
   - It fetches the collection. Because `stream_enabled` is now `true` and no watcher exists, it calls `startWatcher`.
   - The manager loads the collection's sinks from `config.sinks` and registers them with the dispatcher.
   - It reads any existing resume token from `cdc:resume:{name}`.
   - It creates a `Watcher` with the collection's `partition_key`, `sort_key`, and `old_image` settings, then starts the watch loop.
   - It registers the collection with the retry processor.

The API returns `201 Created`.

## Creating a Sink: `POST /api/collections/{name}/sinks`

1. **HTTP binding**: the body is bound into a `collections.Sink`.
2. **API handler** calls `Manager.CreateSink(ctx, name, spec)`.
3. **Domain enforcement**:
   - The collection must exist.
   - The collection must have `stream_enabled = true`.
   - `Type` must be non-empty and `Config` must be present.
   - `EventTypes`, if provided, must be a subset of `{INSERT, MODIFY, REMOVE}`.
   - Type-specific validation is deferred to the sink implementation, not the shared `collections` package.
4. **Persistence**: the sink is inserted into `config.sinks` with a reference to the collection's `_id` (`collection_id`). The generated sink `_id` is returned as `id`.
5. **Notification**: the API publishes the collection name to `cdc:config-change`.
6. **Worker reaction**:
   - The manager receives the notification and refreshes sinks for the collection.
   - It reconciles the currently registered sinks against the new desired set from `config.sinks`.
   - Added or changed sinks are built by `dispatch.BuildSink` and registered; removed sinks are closed and unregistered.
   - If the collection has no active watcher (for example because the notification arrived before stream enablement), the manager simply skips the refresh.

The API returns `201 Created` with the sink body.

## CDC Event Lifecycle

```
MongoDB Change Stream
        │
        ▼
   Watcher (per collection)
        │ parseChange()
        ▼
   StreamRecord
        │
        ▼
   Manager.handleEvent()
   ├── idempotency check (Redis)
   ├── dispatcher.Dispatch()
   │       └── each Sink.Send()
   └── on failure: queueRetry()
                │
                ▼
        Redis retry queue (sorted set)
                │
                ▼
        Retry Processor (polled)
                │
                ├── success: remove from queue
                ├── failure (below max): re-queue with backoff
                └── failure (max exceeded): SendToDLQ + remove
```

### Step-by-step

1. **MongoDB emits a change event** for an insert, update, replace, or delete in the watched collection.
2. **The watcher goroutine** reads the event from the change stream cursor.
3. **`parseChange`** normalizes the event:
   - `insert` → `INSERT` with `newImage`
   - `update` / `replace` → `MODIFY` with `newImage` and optional `oldImage`
   - `delete` → `REMOVE` with optional `oldImage`
   - `drop` / `invalidate` → cancels the watcher and stops gracefully
4. **The manager's handler** receives the `StreamRecord`.
5. **Idempotency**: the manager uses the event ID derived by the watcher from change-stream data — the resume token (`_id._data`), with `clusterTime` + `documentKey` as fallback — and checks `cdc:processed:{id}` in Redis. If present, the event is skipped.
6. **Dispatch**: the dispatcher fans the record out to all sinks registered for that collection. Each sink may filter by event type and by image criteria before sending.
7. **Success path**: if all sinks succeed, the manager marks the event as processed with a 24-hour TTL and the watcher saves the change stream resume token to Redis.
8. **Failure path**: if any sink fails, the manager marshals the record to JSON and enqueues a `RetryEvent` into the Redis sorted set `cdc:retry:{collection}` with `retryCount = 0` and `nextRetryAt = now + 1s`.
9. **Retry processor** wakes up every interval, dequeues events whose `nextRetryAt` has passed, and attempts dispatch again.
10. **Retry outcome**:
    - Success: remove the event from the retry queue.
    - Failure below max retries: increment `retryCount`, recompute `nextRetryAt` with exponential backoff, remove the old member, and add the updated member.
    - Failure at or above max retries: push the raw event to `cdc:dlq:{collection}`, then remove it from the retry queue.

Resume tokens are updated after every successful event, never after a failure. A failure does not skip the event; it moves it to the retry path while the watcher continues consuming new changes.

---

# Configuration Model

## `config.collections`

Stored in MongoDB as `config.collections`.

| Field                 | Type      | Meaning                                                                   |
| --------------------- | --------- | ------------------------------------------------------------------------- |
| `_id`                 | ObjectID  | Internal identifier, also used as `collection_id` in sinks.               |
| `collection_name`     | string    | The MongoDB collection name. Unique.                                      |
| `partition_key`       | string    | Optional partition key field name (DynamoDB-compatible mode).             |
| `sort_key`            | string    | Optional sort key field name. Requires `partition_key`.                   |
| `stream_enabled`      | bool      | Whether CDC is active. Default `false`.                                   |
| `old_image`           | bool      | Whether to include pre-images. Only meaningful when streaming is enabled. |
| `ttl_attribute`       | string    | Optional document field used for MongoDB TTL index.                       |
| `deletion_protection` | bool      | Whether the collection can be deleted. Default `true` on create.          |
| `created_at`          | timestamp | Creation time.                                                            |
| `updated_at`          | timestamp | Last mutation time.                                                       |

### Why these fields

- `partition_key` / `sort_key` define the logical primary key for DynamoDB semantics without touching `_id`.
- `stream_enabled` is the single opt-in flag for CDC. No watcher is created unless this is `true`.
- `old_image` is stored with the stream flag because changing it requires reopening the change stream.
- `ttl_attribute` is stored separately from the stream settings because TTL applies to document expiration, not event shape.
- `deletion_protection` guards against accidental loss of both configuration and data.

## `config.sinks`

Stored in MongoDB as `config.sinks`.

| Field             | Type                  | Meaning                                                                 |
| ----------------- | --------------------- | ----------------------------------------------------------------------- |
| `_id`             | ObjectID              | Sink identifier, exposed as `id`.                                       |
| `collection_id`   | string (ObjectID hex) | Reference to `config.collections._id`. Not exposed.                     |
| `type`            | string                | Sink type: `http`, `eventbridge`, `meilisearch`.                        |
| `spec`            | object                | Opaque, type-specific spec. Interpreted by the sink package.          |
| `event_types`     | []string              | Subset of `INSERT`, `MODIFY`, `REMOVE`. Empty means all.                |
| `filter`          | object                | Per-image filters (`old_image`, `new_image`).                           |
| `created_at`      | timestamp             | Creation time.                                                          |
| `updated_at`      | timestamp             | Last mutation time.                                                     |

### Why `spec` Is Opaque

The shared `Sink` model deliberately stores type-specific settings as an opaque `spec` object rather than a flat set of fields. Each sink implementation owns its own spec struct and decodes `spec` itself. This means adding a new sink type never requires modifying the shared schema or existing sink implementations.

### Why Sinks Are Stored Separately

A collection can have many sinks. Embedding them in the collection document would create unbounded arrays, complicate atomic updates, and make sink-level access control harder. A separate collection with `collection_id` is a normalized design: each sink is an independent configuration resource owned by exactly one collection.

---

# API Design

## REST Philosophy

The API treats configuration as a set of resources:

- `Collection` is a top-level resource under `/api/collections`.
- `Stream`, `TTL`, `Deletion Protection`, and `Sink` are sub-resources under a collection.

Sub-resources are toggled or created/deleted explicitly. There is no generic `PUT /api/collections/{name}` that overwrites the whole collection document.

## Why Configuration Resources Are Immutable

Several configuration fields are immutable once set:

- **Stream settings**: changing `old_image` requires reopening the MongoDB change stream. The code enforces immutability by rejecting `EnableStream` when `stream_enabled` is already `true`.
- **TTL attribute**: changing the TTL field would require dropping and recreating the TTL index. `SetTTL` rejects a second call.
- **Deletion protection when already enabled**: `EnableDeletionProtection` is idempotent only in the sense that disabling is required first; re-enabling while already enabled returns a conflict.

This design makes configuration changes explicit and auditable. Operators must perform a disable/create cycle instead of silently mutating behavior.

## Why Changing Configuration Requires DELETE + POST

Because sub-resources are immutable, the pattern is:

1. `DELETE` the existing sub-resource (e.g., delete stream, delete TTL).
2. `POST` the new sub-resource.

This matches the resource model: a stream or TTL either exists or does not. A `PUT` would imply partial modification, which the domain deliberately disallows.

## Why POST Is Used Instead of PUT

- `POST /api/collections/{name}/stream` creates a stream resource.
- `POST /api/collections/{name}/ttl` creates a TTL resource.
- `POST /api/collections/{name}/sinks` creates a sink resource.
- `POST /api/collections/{name}/protection` enables a protection flag.

These are creation operations on sub-resources, not replacements of the parent collection. Using `POST` keeps the semantics clear: each call adds or enables a specific feature.

## Why DELETE Removes Configuration Resources

`DELETE` on a sub-resource removes that feature:

- `DELETE /api/collections/{name}/stream` disables streaming and clears `old_image`.
- `DELETE /api/collections/{name}/ttl` drops the TTL index and clears `ttl_attribute`.
- `DELETE /api/collections/{name}/sinks/{id}` removes one sink.
- `DELETE /api/collections/{name}/protection` disables deletion protection.
- `DELETE /api/collections/{name}` deletes the collection itself (only if unprotected).

## Endpoint Reference

| Method | Endpoint                               | Purpose                                           |
| ------ | -------------------------------------- | ------------------------------------------------- |
| GET    | `/health`                              | Liveness probe.                                   |
| GET    | `/api/collections`                     | List all collection configurations.               |
| POST   | `/api/collections`                     | Create a collection (name + optional key schema). |
| GET    | `/api/collections/:name`               | Get one collection.                               |
| DELETE | `/api/collections/:name`               | Delete a collection if not protected.             |
| POST   | `/api/collections/:name/stream`        | Enable streaming with `old_image`.                |
| DELETE | `/api/collections/:name/stream`        | Disable streaming.                                |
| POST   | `/api/collections/:name/ttl`           | Enable TTL on a field.                            |
| DELETE | `/api/collections/:name/ttl`           | Disable TTL.                                      |
| POST   | `/api/collections/:name/protection`    | Enable deletion protection.                       |
| DELETE | `/api/collections/:name/protection`    | Disable deletion protection.                      |
| GET    | `/api/collections/:name/sinks`         | List sinks for a collection.                      |
| POST   | `/api/collections/:name/sinks`         | Create a sink.                                    |
| DELETE | `/api/collections/:name/sinks/:id`     | Delete a sink.                                    |
| GET    | `/api/collections/:name/documents`     | List documents.                                   |
| GET    | `/api/collections/:name/documents/:id` | Get a document by `_id`.                          |

---

# Domain Rules

The following invariants are enforced by the code:

- **Collection names are unique**. Enforced by a unique index on `config.collections.collection_name`.
- **A collection name is required** when creating a collection.
- **If `sort_key` is defined, `partition_key` is required**.
- **`partition_key` and `sort_key` cannot be the same field**.
- **Key field names are configurable and never hard-coded as `pk`/`sk`**. The code uses whatever names the operator provides.
- **`_id` is managed by MongoDB and is never derived from key fields**. Key fields are stored explicitly on documents.
- **Deletion protection is enabled by default** on collection creation. The create handler overwrites any caller-provided value with `true`.
- **A protected collection cannot be deleted**. `Manager.Delete` returns `ErrDeletionProtectionEnabled` unless protection is first disabled.
- **Stream configuration is immutable while enabled**. `EnableStream` returns `ErrStreamAlreadyExists` if `stream_enabled` is already `true`, regardless of the requested `old_image` value.
- **Disabling a stream resets `stream_enabled` and `old_image` to `false`**. This allows redefinition.
- **TTL configuration is immutable while set**. `SetTTL` returns `ErrTTLAlreadyExists` if `ttl_attribute` is already non-empty.
- **A sink belongs to exactly one collection**. Enforced by the `collection_id` reference and by scoping sink reads/deletes to that collection.
- **A sink can only be created if streaming is enabled** for its collection.
- **Sink event types, when specified, must be `INSERT`, `MODIFY`, or `REMOVE`**.
- **A sink must have a non-empty `type` and a non-empty `spec` object**. Type-specific required fields are validated by the sink implementation, not the shared model.
- **A watcher exists only for stream-enabled collections**. The manager starts watchers only for collections with `stream_enabled = true`.
- **There is at most one watcher per collection**. The manager's registry is keyed by collection name.
- **Resume tokens are isolated per collection**. Key format: `cdc:resume:{collectionName}`.
- **Resume tokens advance only after successful processing**. Failures route events to retry; the change stream cursor still advances, but the saved token reflects the last successfully handled event.
- **Resume tokens are never deleted on generic errors**. Transient failures (network, elections, cursor timeouts) preserve the token so the watcher resumes from the last successful position and skips nothing. The token is invalidated only when MongoDB explicitly rejects it as invalid (unparseable token, or a token from a dropped and recreated collection).
- **Idempotency is required for all event processing**. Duplicate event IDs within the 24-hour TTL are skipped.
- **Event IDs are deterministic and derived exclusively from change-stream data**. The primary source is the resume token (`_id._data`); `clusterTime` + `documentKey` serve as fallback. Application-generated timestamps (e.g. `time.Now()`) are never part of the ID, so the same MongoDB change produces the same ID across process restarts.
- **Event ordering is not guaranteed**. Downstream consumers must be eventually consistent.
- **Retry uses exponential backoff capped at 5 minutes** with a maximum of 5 attempts.
- **Events exhausted from retry go to the DLQ**. Key format: `cdc:dlq:{collectionName}`.
- **Sinks are closed when removed or when the collection is cleared**.
- **Deleting a collection drops the MongoDB collection and cascades deletion to its sinks**.

---

# Watcher Manager

`watcher.Manager` is the worker's control loop. It owns the map of active watchers and keeps it synchronized with `config.collections`.

## Startup

1. `Manager.Start(ctx)` loads all stream-enabled collections via `ListStreamEnabled`.
2. For each enabled collection, it calls `startWatcher`.
3. It subscribes to Redis Pub/Sub on `cdc:config-change`.
4. It starts a background sync loop that polls `config.collections` at `SyncInterval` (default 30 seconds in code; README mentions 15 minutes, but `DefaultConfig` uses 30 seconds).

## Synchronization

`syncWithCollections` runs on every tick:

1. Fetches stream-enabled collections from MongoDB.
2. Compares them with the current watcher registry.
3. Starts watchers for new collections.
4. For existing watchers:
   - If `old_image` changed, restarts the watcher (because the change stream options must change).
   - Refreshes sinks (see below).
5. Stops watchers for collections that are no longer enabled.

## Pub/Sub Notifications

Whenever the API mutates configuration, it publishes the affected collection name to `cdc:config-change`. The manager's `configChangeLoop` receives these messages and calls `handleCollectionChange`, which performs the same start/stop/restart/refresh logic as the periodic sync but immediately.

## Why Pub/Sub Exists Even Though Polling Exists

- **Latency**: Polling is bounded by the sync interval. Pub/Sub lets the worker react to configuration changes within roughly a second.
- **Resilience**: If Pub/Sub fails, polling is the fallback. If polling is slow, Pub/Sub provides immediate feedback.
- **Operational simplicity**: The periodic sync guarantees eventual consistency even if the Redis channel is missed or the worker reconnects.

## Restart Behavior

- `restartWatcher` stops the existing watcher and starts a new one with fresh configuration.
- On a MongoDB error, the watcher deletes its resume token, waits 5 seconds, and loops. The next `watchOnce` call starts from the latest position.
- If the collection is dropped or the change stream is invalidated, the watcher cancels itself and exits cleanly.

## Sink Refresh

`refreshSinks` loads the current sink specs from `config.sinks`, reconciles them against the previously registered set, and applies only the changes:

- New sinks are built and registered.
- Removed sinks are closed and unregistered.

Sink identity is based on the persisted sink ID. Because sinks are immutable, a change to any field inside `spec` is naturally observed as the deletion of one sink and the creation of another.

## Resume Tokens

Resume tokens are fetched from Redis when a watcher starts and saved to Redis after each successfully processed event. When a watcher restarts because of an `old_image` change or a transient error, it picks up the last token. If the token is invalid, the watcher clears it and resumes from the latest position.

## Retry Registration

When a watcher is started, the manager calls `retryProcessor.RegisterCollection`. When a watcher is stopped, it calls `UnregisterCollection`. This tells the retry processor which queues to poll, preventing it from scanning queues for collections that no longer exist.

---

# Error Handling

## Domain Errors

`internal/collections/errors.go` defines sentinel errors for every predictable domain failure:

- `ErrCollectionNotFound`
- `ErrDocumentNotFound`
- `ErrSinkNotFound`
- `ErrDeletionProtectionEnabled`
- `ErrValidation`
- `ErrStreamAlreadyExists`
- `ErrTTLAlreadyExists`
- `ErrProtectionAlreadyExists`

Callers identify these with `errors.Is`, never by string matching.

## API Translation

`internal/api/errors.go` provides a single function `responseFor(err)` that maps any error to an HTTP status, machine-readable `code`, and human-readable `message`:

| Domain Error                                 | HTTP Status | Code                          |
| -------------------------------------------- | ----------- | ----------------------------- |
| Bad request (malformed JSON, unknown fields) | 400         | `invalid_request`             |
| `ErrValidation`                              | 400         | `validation_error`            |
| `ErrCollectionNotFound`                      | 404         | `collection_not_found`        |
| `ErrDocumentNotFound`                        | 404         | `document_not_found`          |
| `ErrSinkNotFound`                            | 404         | `sink_not_found`              |
| `ErrDeletionProtectionEnabled`               | 403         | `deletion_protection_enabled` |
| `ErrStreamAlreadyExists`                     | 409         | `stream_already_exists`       |
| `ErrTTLAlreadyExists`                        | 409         | `ttl_already_exists`          |
| `ErrProtectionAlreadyExists`                 | 409         | `protection_already_exists`   |
| Unknown error                                | 500         | `internal_error`              |

## HTTP Responses

Every API error uses the same JSON envelope:

```json
{
  "error": {
    "code": "...",
    "message": "..."
  }
}
```

The API logs Redis Pub/Sub failures but never returns them to the client, because notifications are best-effort.

---

# Internal Design Principles

The following principles are reflected in the codebase:

- **API contains no business rules.** Every invariant lives in `internal/collections`. The API only binds, calls, and translates.
- **Business logic lives in the domain.** `collections.Manager` is the sole authority for configuration mutations.
- **Infrastructure is isolated.** MongoDB and Redis are wrapped in dedicated packages that expose domain-shaped operations rather than raw driver methods.
- **Configuration is the source of truth.** The worker does not hard-code which collections to watch; it reconciles its runtime state against `config.collections`.
- **Resources are immutable whenever possible.** Stream, TTL, and protection sub-resources are immutable once set; changing them requires explicit deletion and recreation.
- **Small focused files.** Each Go file has one responsibility: one handler per API file, one concept per collections file, one sink implementation per file.
- **Explicit behavior over magic.** There are no hidden watchers, no automatic sink enablement, and no implicit defaults that override operator intent.
- **No global mutable state.** All components receive dependencies through constructors.
- **Context-driven concurrency.** Watchers, retry loops, and managers are started with a context and stop when it is cancelled.
- **No panic in production.** Errors are returned or logged; the worker degrades rather than crash.
- **Prefer composition over inheritance-like abstractions.** Sinks are registered via a builder registry; the dispatcher, watcher, and manager are composed from small interfaces.
- **Favor simple concurrency patterns.** A mutex-protected map and per-watcher goroutines are used instead of complex actor systems.

---

# Directory Structure

## `cmd/`

Entry points for the two runtime processes.

- `cmd/api/main.go`: Loads configuration, initializes MongoDB and Redis, creates `collections.Manager`, and starts the Gin HTTP server.
- `cmd/worker/main.go`: Loads configuration, initializes infrastructure, creates the dispatcher, retry processor, and watcher manager, and runs until a shutdown signal.

  **Graceful shutdown.** On SIGINT or SIGTERM the worker shuts down in dependency order, bounded by `SHUTDOWN_TIMEOUT` (default 30s): (1) the watcher manager is stopped first — cancelling its run context, waiting for its sync/config-change loops and every watcher, and closing pub/sub — so no new events flow while in-flight bookkeeping drains; (2) the retry processor is stopped, letting the current pass finish; (3) the dispatcher closes all sinks/transports; (4) Redis is closed; (5) MongoDB is closed. Terminal bookkeeping writes (resume-token persist, `MarkProcessed`, retry `Enqueue`/`Remove`) and change-stream cursor close use a short detached context so a mid-flight event is never lost when the live context is cancelled. No arbitrary sleeps are used; shutdown is driven entirely by context cancellation and `sync.WaitGroup` waits. Panics in worker goroutines are contained: each long-running goroutine has a recover backstop that logs with a stack trace, per-event/per-tick work is panic-isolated so a single bad event cannot kill its loop, and a panicking watcher marks itself stopped for the manager's sync to reconcile.

## `internal/api/`

HTTP layer. Each file handles one resource or concern:

- `server.go`: Server struct, dependency wiring, route registration, config-change publisher.
- `router.go`: Gin route definitions.
- `collections.go`: Collection list/create/get/delete handlers.
- `streams.go`: Stream enable/disable handlers.
- `sinks.go`: Sink list/create/delete handlers.
- `ttl.go`: TTL enable/disable handlers.
- `protection.go`: Deletion protection enable/disable handlers.
- `documents.go`: Read-only document list/get handlers.
- `health.go`: Health check.
- `bind.go`: Strict and non-strict JSON binding helpers.
- `errors.go`: Canonical error response mapping.

The package deliberately contains no business logic beyond HTTP translation.

## `internal/collections/`

Domain package for configuration.

- `collection.go`: `Collection` struct, `Manager` store, collection CRUD, physical MongoDB collection creation, key index management.
- `stream.go`: Stream enable/disable with immutability and `changeStreamPreAndPostImages` configuration.
- `sink.go`: `Sink` and `Sink` structs (common metadata only), sink CRUD, shared validation.
- `ttl.go`: TTL index creation/removal with immutability.
- `protection.go`: Deletion protection toggle with conflict detection.
- `document.go`: Read-only document access.
- `filter.go`: Per-image filter matching for sink filtering.
- `errors.go`: Sentinel domain errors.
- `doc.go`: Package documentation.
- Test files mirror the source files.

## `internal/watcher/`

Worker's watcher lifecycle management.

- `manager.go`: `Manager`, sync loop, Pub/Sub listener, start/stop/restart logic, sink refresh.
- `watcher.go`: `Watcher`, the per-collection change stream consumer.
- `reconciliation.go`: Sink reconciliation and incremental updates.
- `doc.go`: Package documentation.

## `internal/dispatch/`

Event routing and sink registry.

- `dispatcher.go`: `Dispatcher` with concurrent-safe sink registry.
- `sink.go`: `Sink` interface, builder registry, `BuildSink` factory.
- `sinks/http.go`: Fully implemented HTTP webhook sink with its own `HTTPSpec`.
- `transports/eventbridge.go`: Fully implemented EventBridge sink with its own `EventBridgeSpec` (AWS SDK v2 PutEvents, SDK-resolved region). The region comes from the AWS SDK default region chain (`AWS_REGION`, shared config, or the compute environment) — never the spec. Credentials come from the AWS SDK default credential chain — never the spec — and construction fails fast if none resolve.
- `sinks/meilisearch.go`: Fully implemented Meilisearch sink with its own `MeilisearchSpec` (official meilisearch-go SDK; awaits task completion). Documents are keyed by their MongoDB `_id` (the index primary key).
- `sinks/doc.go`: Sink package overview.

## `internal/retry/`

Retry queue processing.

- `processor.go`: Polling loop, backoff calculation, DLQ routing.
- `doc.go`: Package documentation.

## `internal/redis/`

Redis wrapper.

- `client.go`: Connection, key helpers, resume token, idempotency, retry/DLQ, Pub/Sub.
- `doc.go`: Package documentation.

## `internal/mongo/`

MongoDB wrapper.

- `client.go`: Connection, readiness wait (writable PRIMARY + client Ping), collection access.
- `doc.go`: Package documentation.

## `internal/streams/`

Canonical CDC record format.

- `stream.go`: `StreamRecord`, `RecordType`, parsing helper.
- `doc.go`: Package documentation.

## `internal/config/`

Environment-based configuration loading.

- `config.go`: `Config` struct and `Load()`.

## `ui/`

Web UI built separately and served by the `ui` Docker Compose service.

---

# Future Extensions

The architecture already contains clear extension points:

## New Sink Implementations

Adding a sink requires:

1. Creating a new file in `internal/dispatch/sinks/`.
2. Defining a type-specific `Config` struct for the sink's own settings.
3. Implementing the `dispatch.Sink` interface.
4. Calling `dispatch.RegisterSink("type", builder)` in an `init()` function.
5. Ensuring `cmd/worker/main.go` imports the package with a blank import (already done for the `sinks` package).

Because the shared `Sink` model stores type-specific settings as an opaque `spec` object, adding a new sink type never requires modifying the shared schema or existing sink implementations. The builder decodes and validates its own `spec` payload.

The HTTP sink is the reference implementation. EventBridge and Meilisearch are fully implemented with their official SDKs (AWS SDK v2 and meilisearch-go respectively).

## Additional Dispatchers

The watcher manager depends on the small `Dispatcher` interface (`Dispatch(ctx, collection, record) error`). A custom dispatcher—for example one that writes to a message bus or fans out to a sidecar—can be injected without changing the watcher code.

## New Retry Strategies

`retry.Processor` accepts a `Config` with `Interval`, `MaxRetries`, `BaseDelay`, and `MaxDelay`. A future change could make the backoff strategy pluggable (linear, jittered, circuit-breaker) while keeping the queue operations unchanged.

## New Watcher Implementations

The manager's `startWatcher` method instantiates `watcher.Watcher` directly today, but the watcher is a small struct with a clear lifecycle. A future design could introduce a `WatcherFactory` interface to support collection-specific watchers (for example, a watcher that batches events before dispatch).

## New API Surfaces

Because all business rules live in `collections.Manager`, a gRPC service, a Terraform provider, or a future UI backend can reuse the same domain layer without duplicating invariants.

## DLQ Replay

The DLQ is currently a final destination. A future extension could add an operator endpoint to replay DLQ events back into the retry queue or directly to sinks.

---

# Summary

Conduit is a small, deliberate system that separates control from data. The API owns configuration and publishes change notifications; the worker realizes that configuration by opening MongoDB change streams, transforming changes into a DynamoDB-style record format, and dispatching them to pluggable sinks. Redis is the worker's memory: resume tokens, retry queues, DLQ, idempotency, and Pub/Sub.

The design is conservative and explicit. Configuration is immutable where changing it would require infrastructure side effects; sub-resources are created and deleted explicitly; errors are domain-first and translated at the edge; watchers are reconciled from the database rather than hard-coded. The result is a stateless, horizontally scalable data plane that can survive restarts, transient downstream failures, and invalid resume tokens without losing events.

In short: Conduit gives MongoDB a DynamoDB-shaped CDC experience, one explicitly configured collection at a time.
