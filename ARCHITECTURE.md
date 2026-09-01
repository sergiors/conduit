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

- **DynamoDB-compatible mode**: the operator defines a `partitionKey` and an optional `sortKey`. Conduit creates a unique composite index on those fields and treats them as the logical primary key for CDC records.
- **MongoDB-native mode**: no key schema is defined. Documents are identified purely by MongoDB `_id`, and no DynamoDB-style key index is created.

## Collection Manager

_Collection Manager_ is the domain service (`internal/collections.Manager`) that owns every configuration mutation and the physical MongoDB infrastructure behind it. It is the only place where the invariants around collections, streams, sinks, TTL, and deletion protection are enforced. The API layer delegates all business decisions to it.

## Document

A _document_ is a record inside a MongoDB collection. Conduit does not write documents on behalf of callers through the API; it only reads them via `GET /api/collections/{name}/documents`. The worker, however, observes every insert, update, replace, and delete performed by application code that talks directly to MongoDB.

## Stream

A _stream_ is the CDC subscription for a collection. Streams are opt-in. A collection has a stream only when `streamEnabled` is `true`. Enabling a stream also decides whether the worker requests the pre-image (`oldImage`) of each change from MongoDB.

Because `oldImage` affects how the MongoDB change stream is opened, stream configuration is immutable once enabled. To change it, the operator must disable the stream and then re-enable it.

Enabling a stream captures a **first-start checkpoint**: a MongoDB cluster timestamp (taken from the API host clock with increment 1) persisted on the collection document as `streamStartedAt`. The checkpoint's only job is to anchor the FIRST watcher run: with no resume token yet, the watcher opens its change stream with `startAtOperationTime == streamStartedAt`, streaming every event from enablement instead of "now". This closes the enable → watcher-start window that otherwise silently dropped events written there. Once the first event is settled, normal per-event resume-token persistence takes over and the checkpoint is no longer consulted (a resume token always wins). The checkpoint is derived from the API host clock, so it relies on the operational assumption that the API host clock is reasonably aligned with the MongoDB cluster clock; small skew only shifts the replay anchor, never drops a token-covered event. `DisableStream` clears the checkpoint; re-enabling captures a fresh one.

## Sink

A _sink_ is a destination for CDC events. Each sink belongs to exactly one collection and can filter by event type (`INSERT`, `MODIFY`, `REMOVE`) and by content of the `newImage` / `oldImage` using the filter DSL: `eq`, `ne`, `gt`, `gte`, `lt`, `lte`, `contains`, `startsWith`, `endsWith`, `exists`, `in`, and `notIn`. See [`docs/filter.md`](docs/filter.md) for the full DSL reference.

The shared `Sink` model carries only common metadata: `type`, an opaque `spec` payload, `eventTypes`, and `filter`. Type-specific settings (endpoint, region, host, etc.) live inside `spec` and are owned by the individual sink implementation. This keeps the shared model stable as new sink types are added.

Sinks are persisted separately from collections in `config.sinks` so that a collection can have many sinks without bloating the collection document. The worker loads sinks when it starts or refreshes a watcher.

### Filter Semantics

`filter` is purely **declarative** — a set of conditions an event must satisfy to be delivered to that sink. Evaluation (`internal/collections.Filter`, applied in `dispatch.RuntimeSink.Send`; full reference in [`docs/filter.md`](docs/filter.md)):

- **No filter block declared** (`oldImage` or `newImage` absent from `filter`): that image is not inspected at all — the block is ignored.
- **Empty filter block declared** (`"newImage": {}`): matches every event that has that image.
- **Every declared criterion must match** (AND across fields and within a field's conditions). An event is delivered only if _all_ declared criteria match.
- **A declared filter block whose corresponding image is absent evaluates to `false`.** A `REMOVE` event has no `newImage`; an image the collection does not record (`oldImage=false`) is always absent. This is the intended semantics: "match on the content of `oldImage`" logically requires an `oldImage` to exist — an absent image cannot satisfy a content predicate, the same way EventBridge patterns do not match missing fields.
- **Flat, AND-only predicates.** Filters are flat AND-only predicates per image block: every declared criterion must match. Recursive logical groups (`and` / `or`) were implemented and subsequently **removed**; boolean composition is intentionally delegated to multiple sinks — a design choice of simplicity over expression power.

Sink filters are **declarative and intentionally decoupled from the collection's current configuration**: creation does not reject an `oldImage` filter when the collection's stream currently has `oldImage=false`, nor `newImage` criteria that cannot match `REMOVE` events. Configuration is immutable per stream cycle, but it _can_ change over the sink's lifetime (disable stream → re-enable with `oldImage=true`), and a sink that already encodes its pre-image requirements is then correct without modification. Coupling sink definitions to the collection's current mode would turn a transient configuration state into a permanent restriction and break that forward compatibility. The consequence is deliberate: when the corresponding image is absent, every declared criterion fails and the event is silently not delivered — if an image is essential to the filter, also subscribe to the event types that carry it (`eventTypes`, plus `{"oldImage": {"exists": true}}`-style predicates where appropriate) and/or enable `oldImage` on the collection.

## Dispatcher

The _dispatcher_ (`internal/dispatch.Dispatcher`) is the event router inside the worker. It maintains a registry of `collection → []RuntimeSink`. When a change stream record arrives, the dispatcher fans the record out to every sink registered for that collection **in parallel**, delivering through each sink's own execution lane (a bounded job queue + worker pool), and waits for every delivery to settle. It tolerates individual sink failures: one failing (or slow) sink does not prevent others from receiving the event, but the dispatcher returns an error so the caller can decide to retry. See the detailed [Dispatcher](#dispatcher-1) section below for the concurrency and backpressure model.

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
    └─ streamEnabled = true ──▶ Watcher ──▶ Change Stream ──▶ Dispatcher ──▶ Sinks
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

`dispatch.Dispatcher` routes a `StreamRecord` to every sink registered for the event's collection. It is a concurrent-safe fan-out layer that delivers to all matching sinks **in parallel** while preserving the caller's settlement contract.

### Public API

- `NewDispatcher()` creates a dispatcher using default per-sink lane config.
- `NewDispatcherWithConfig(cfg)` creates a dispatcher with a custom `Config{QueueSize, WorkerCount}`; zero/negative values fall back to the defaults (`QueueSize=1024`, `WorkerCount=4`).
- `Register(collection, sink)` adds a sink for a collection, creating and starting its delivery lane.
- `Remove(collection, name)` removes and closes a single sink's lane.
- `Update(collection, sink)` atomically applies a new persisted config to an existing sink in place, preserving its transport and its running lane.
- `Clear(collection)` removes and closes all lanes for a collection.
- `Dispatch(ctx, collection, record)` sends the record to all registered sinks in parallel and blocks until every matching sink delivery has settled.
- `Close()` stops and closes every lane, idempotently.

### Concurrency and settlement model (the "why")

The dispatcher uses **synchronous settlement with parallel per-sink lanes** — not volatile fire-and-forget.

- **One registered sink ⇒ one lane.** Every `RuntimeSink` owns a lane: a bounded job queue drained by a small worker pool (default 4 workers). Workers call `RuntimeSink.Send(ctx, record)` for each job. Filtering still happens inside `Send`, so a filtered event returns nil and is settled for that sink.
- **Per-event parallel fan-out.** `Dispatch` snapshots the collection's lanes under a read lock, then submits one job per lane **concurrently** (a separate goroutine per lane) so a full or slow sink lane cannot prevent the event from being offered to the other lanes. It then waits for every submission and, after all deliveries complete, returns an aggregate error if any matching sink failed.
- **Isolation by default.** Because each sink has its own queue and worker pool, a slow or blocked sink (e.g. a webhook endpoint that stops responding up to its timeout) cannot hold up delivery to other sinks for the duration of an event.
- **Bounded backpressure, no drops.** Each lane's queue is bounded. When a queue is full, `Dispatch`'s submission for that lane blocks until a worker frees capacity, the caller's context is cancelled, or the lane is closed. Events are never silently dropped; the watcher/retry caller receives backpressure instead.
- **Settlement is unchanged.** `Dispatch` returns nil only when the event was delivered to every matching sink. This is what preserves the pipeline's at-least-once and resume-token guarantees.

### What it Must Never Do

- Decide whether an event should be retried. It only reports whether any sink (or submission) failed.
- Break the settlement contract: returning nil must mean the event was delivered (or filtered) for every matching sink, so the single watcher/resume-token owner and the retry/DLQ semantics are untouched.
- Silently drop an event because a lane queue is full. Full lanes apply backpressure to the caller.
- Block forever on a single worker. Sink implementations own their timeouts (transports must respect `ctx`); the lane drains accepted jobs before closing.

### Why one watcher and one resume-token owner remain

Delivery concurrency lives **inside** `Dispatch`, per event, per sink. It does not create additional change-stream readers: there is still exactly one watcher per enabled collection and exactly one owner of that collection's resume token. The token advances only after the watcher's handler (→ `Dispatch`) returns nil — meaning the event was delivered to every matching sink. A crash after a nil return cannot lose an event the sink already accepted, because nothing is acknowledged by enqueuing into a volatile in-memory queue; every sink queue is drained before `Dispatch` returns nil. Per-sink queues therefore add throughput and failure isolation without weakening durability or introducing a second resume-token owner.

### Dependencies

- `Sink` implementations registered via the transport builder registry in `transports/`.

### Why it Exists

Fan-out is a separate concern from event production and from sink implementation. The dispatcher lets the system add, remove, and update sinks without touching the watcher or retry code, and — with parallel per-sink lanes — scales delivery throughput to multiple sinks without additional change-stream readers or a second resume-token owner.

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

1. **HTTP binding**: `bindStrictJSON` parses only `collectionName`, `partitionKey`, and `sortKey`. Unknown fields are rejected.
2. **API handler** constructs a `collections.Collection` and calls `Manager.Create`.
3. **Domain validation** inside `Manager.Create`:
   - `collectionName` must be non-empty.
   - If `sortKey` is set, `partitionKey` must also be set.
   - `partitionKey` and `sortKey` cannot be the same.
   - `DeletionProtection` is forced to `true`.
   - `CreatedAt` and `UpdatedAt` are set.
4. **Physical collection creation**:
   - If the MongoDB collection does not exist, Conduit creates it.
   - A placeholder document is briefly inserted and removed. Empty collections can cause change-stream issues, so this guarantees the collection is materialized.
   - A unique composite index on the configured key fields is created (or ensured) when a key schema is defined.
5. **Configuration persistence**: the collection document is inserted into `config.collections`, and the generated `_id` is returned to the caller.
6. **Notification**: the API publishes the collection name to the Redis channel `cdc:config-change`.
7. **Worker reaction** (best-effort, via Pub/Sub): the watcher manager sees the notification, fetches the collection, and—because `streamEnabled` is still `false`—takes no watcher action.

The API returns `201 Created` with the collection body.

## Enabling a Stream: `POST /api/collections/{name}/stream`

1. **HTTP binding**: the body must contain `oldImage` (bool, required).
2. **API handler** calls `Manager.EnableStream(ctx, name, oldImage)`.
3. **Domain enforcement**:
   - The update is conditional: it only succeeds when `streamEnabled` is not already `true`.
   - If the update matches no document, Conduit checks whether the collection exists. If it does, `ErrStreamAlreadyExists` is returned; otherwise `ErrCollectionNotFound`.
   - Stream configuration is therefore immutable while enabled.
4. **Physical MongoDB configuration**: when `oldImage` is `true`, `changeStreamPreAndPostImages` is ensured on the collection via `collMod` (idempotent; collections created through Conduit already have it). A failure aborts the enablement and rolls the recorded stream back: enabling a stream with `oldImage` on a deployment that cannot produce pre-images would silently drop every pre-image at the source.
5. **Configuration persistence**: `streamEnabled`, `oldImage`, and the first-start checkpoint `streamStartedAt` (a `primitive.Timestamp` from the API host clock) are updated in `config.collections`. The checkpoint anchors the first watcher run so no event between enablement and watcher start is skipped.
6. **Notification**: the API publishes the collection name to `cdc:config-change`.
7. **Worker reaction**:
   - The watcher manager receives the Pub/Sub message (or discovers the change on the next poll).
   - It fetches the collection. Because `streamEnabled` is now `true` and no watcher exists, it calls `startWatcher`.
   - The manager loads the collection's sinks from `config.sinks` and registers them with the dispatcher.
   - It reads any existing resume token from `cdc:resume:{name}`.
   - It creates a `Watcher` with the collection's `partitionKey`, `sortKey`, `oldImage`, and `start_at_operation_time` (= `streamStartedAt`) settings, then starts the watch loop.
   - It registers the collection with the retry processor.

The API returns `201 Created`.

## Creating a Sink: `POST /api/collections/{name}/sinks`

1. **HTTP binding**: the body is bound into a `collections.Sink`.
2. **API handler** calls `Manager.CreateSink(ctx, name, spec)`.
3. **Domain enforcement**:
   - The collection must exist.
   - The collection must have `streamEnabled = true`.
   - `Type` must be non-empty and `Config` must be present.
   - `EventTypes`, if provided, must be a subset of `{INSERT, MODIFY, REMOVE}`.
   - Type-specific validation is deferred to the sink implementation, not the shared `collections` package.
4. **Persistence**: the sink is inserted into `config.sinks` with a reference to the collection's `_id` (`collectionId`). The generated sink `_id` is returned as `id`.
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
   │       └── for each sink: submit a job to its lane, in parallel
   │               └── lane worker → RuntimeSink.Send()
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
6. **Dispatch**: the dispatcher fans the record out to all sinks registered for that collection **in parallel** — one job per sink lane — and waits for every delivery to settle before returning. Each sink may filter by event type and by image criteria before sending (in `RuntimeSink.Send`, inside its lane worker). A full lane applies backpressure (bounded by queue capacity) instead of dropping; once all lanes settle, the event is durably delivered to every matching sink.
7. **Success path**: if all sinks succeed, the manager marks the event as processed with the configured TTL (default 24h, `PROCESSED_EVENT_TTL`) and the watcher saves the change stream resume token to Redis. These two writes are **not atomic**: if the processed-key or resume-token write fails after dispatch, the event is replayed and delivered again — delivery is at-least-once.
8. **Failure path**: if any sink fails, the manager marshals the record to JSON and enqueues a `RetryEvent` into the Redis sorted set `cdc:retry:{collection}` with `retryCount = 0` and `nextRetryAt = now + 1s`.
9. **Retry processor** wakes up every interval, dequeues events whose `nextRetryAt` has passed, and attempts dispatch again.
10. **Retry outcome**:
    - Success: remove the event from the retry queue.
    - Failure below max retries: increment `retryCount`, recompute `nextRetryAt` with exponential backoff, remove the old member, and add the updated member.
    - Failure at or above max retries: push the raw event to `cdc:dlq:{collection}`, then remove it from the retry queue.

Resume tokens are updated after every successful event, never after a failure. A failure does not skip the event; it moves it to the retry path while the watcher continues consuming new changes. Because the processed-key write and the resume-token write are not atomic, and because the processed key expires after `PROCESSED_EVENT_TTL` (default 24h), delivery is **at-least-once**: a duplicate can be delivered after a crash, a Redis outage, or downtime longer than the TTL. Downstream consumers must be idempotent using the deterministic `eventID`.

---

# Configuration Model

## `config.collections`

Stored in MongoDB as `config.collections`.

| Field                 | Type      | Meaning                                                                   |
| --------------------- | --------- | ------------------------------------------------------------------------- |
| `_id`                 | ObjectID  | Internal identifier, also used as `collectionId` in sinks.               |
| `collectionName`     | string    | The MongoDB collection name. Unique.                                      |
| `partitionKey`       | string    | Optional partition key field name (DynamoDB-compatible mode).             |
| `sortKey`            | string    | Optional sort key field name. Requires `partitionKey`.                   |
| `streamEnabled`      | bool      | Whether CDC is active. Default `false`.                                   |
| `oldImage`           | bool      | Whether to include pre-images. Only meaningful when streaming is enabled. |
| `ttlAttribute`       | string    | Optional document field used for MongoDB TTL index.                       |
| `deletionProtection` | bool      | Whether the collection can be deleted. Default `true` on create.          |
| `createdAt`          | timestamp | Creation time.                                                            |
| `updatedAt`          | timestamp | Last mutation time.                                                       |

### Why these fields

- `partitionKey` / `sortKey` define the logical primary key for DynamoDB semantics without touching `_id`.
- `streamEnabled` is the single opt-in flag for CDC. No watcher is created unless this is `true`.
- `oldImage` is stored with the stream flag because changing it requires reopening the change stream.
- `ttlAttribute` is stored separately from the stream settings because TTL applies to document expiration, not event shape.
- `deletionProtection` guards against accidental loss of both configuration and data.

## `config.sinks`

Stored in MongoDB as `config.sinks`.

| Field           | Type                  | Meaning                                                                                       |
| --------------- | --------------------- | --------------------------------------------------------------------------------------------- |
| `_id`           | ObjectID              | Sink identifier, exposed as `id`.                                                             |
| `collectionId` | string (ObjectID hex) | Reference to `config.collections._id`. Not exposed.                                           |
| `type`          | string                | Sink type: `http`, `eventbridge`, `meilisearch`. **Immutable** (set at creation).             |
| `spec`          | object                | Opaque, type-specific spec. Interpreted by the sink package. **Immutable** (set at creation). |
| `eventTypes`   | []string              | Subset of `INSERT`, `MODIFY`, `REMOVE`. Empty means all. **Mutable** via PATCH.               |
| `filter`        | object                | Per-image filters (`oldImage`, `newImage`). **Mutable** via PATCH.                          |
| `fingerprint`   | string                | Deterministic hash of the sink's immutable identity (type + spec); server-computed.           |
| `createdAt`    | timestamp             | Creation time.                                                                                |
| `updatedAt`    | timestamp             | Last mutation time.                                                                           |

A sink's **fingerprint** is a deterministic SHA-256 hash of its **immutable
identity** — `type` and `spec` canonicalized (map keys sorted, timestamps and
ids excluded) — so two sinks that deliver to the same destination produce the
same fingerprint regardless of JSON ordering or administrative fields. It
deliberately **excludes** the mutable behavior (`filter`, `eventTypes`): those
are updated in place via PATCH and must not change the destination identity. The
fingerprint backs duplicate prevention:
`CreateSink` rejects a second sink with the same fingerprint for the same
collection (`409 sink_already_exists`), because two sinks with the same
destination would be ambiguous. The check is race-safe via a unique compound
index on (`collectionId`, `fingerprint`). Because `type`/`spec` are immutable,
PATCH can never collide with this index.

The split is intentional: **immutable** fields (`type`, `spec`) are _where_ a
sink delivers events — changing them requires creating a new sink; **mutable**
fields (`filter`, `eventTypes`) are _how/when_ it delivers, updated
live via PATCH. Events keep flowing while an update lands.

### Why `spec` Is Opaque

The shared `Sink` model deliberately stores type-specific settings as an opaque `spec` object rather than a flat set of fields. Each sink implementation owns its own spec struct and decodes `spec` itself. This means adding a new sink type never requires modifying the shared schema or existing sink implementations.

### Why Sinks Are Stored Separately

A collection can have many sinks. Embedding them in the collection document would create unbounded arrays, complicate atomic updates, and make sink-level access control harder. A separate collection with `collectionId` is a normalized design: each sink is an independent configuration resource owned by exactly one collection.

---

# API Design

## REST Philosophy

The API treats configuration as a set of resources:

- `Collection` is a top-level resource under `/api/collections`.
- `Stream`, `TTL`, `Deletion Protection`, and `Sink` are sub-resources under a collection.

Sub-resources are toggled or created/deleted explicitly. There is no generic `PUT /api/collections/{name}` that overwrites the whole collection document.

## Why Configuration Resources Are Immutable

Several configuration fields are immutable once set:

- **Stream settings**: changing `oldImage` requires reopening the MongoDB change stream. The code enforces immutability by rejecting `EnableStream` when `streamEnabled` is already `true`.
- **TTL attribute**: changing the TTL field would require dropping and recreating the TTL index. `SetTTL` rejects a second call.
- **Deletion protection when already enabled**: `EnableDeletionProtection` is idempotent only in the sense that disabling is required first; re-enabling while already enabled returns a conflict.

This design makes configuration changes explicit and auditable. Operators must perform a disable/create cycle instead of silently mutating behavior.

**Sinks are the intentional exception.** A sink's _identity_ (`type`, `spec` — where events go) is immutable and follows the delete/recreate pattern, but its _behavior_ (`filter`, `eventTypes` — how/when events are delivered) is mutable and updated live via `PATCH /api/collections/{name}/sinks/{id}`. Attempting to change `type`/`spec` via PATCH returns `400 sink_identity_immutable`. This split lets operators tune delivery (filtering, event types) without touching the destination, while keeping the "where events go" identity stable and audited.

## Why Changing Configuration Requires DELETE + POST

Because sub-resources are immutable, the pattern is:

1. `DELETE` the existing sub-resource (e.g., delete stream, delete TTL).
2. `POST` the new sub-resource.

This matches the resource model: a stream or TTL either exists or does not. A `PUT` would imply partial modification, which the domain deliberately disallows. Sinks use `PATCH` for their mutable fields instead; only immutable identity changes (`type`/`spec`) require delete/recreate.

## Why POST Is Used Instead of PUT

- `POST /api/collections/{name}/stream` creates a stream resource.
- `POST /api/collections/{name}/ttl` creates a TTL resource.
- `POST /api/collections/{name}/sinks` creates a sink resource.
- `POST /api/collections/{name}/protection` enables a protection flag.

These are creation operations on sub-resources, not replacements of the parent collection. Using `POST` keeps the semantics clear: each call adds or enables a specific feature.

## Why DELETE Removes Configuration Resources

`DELETE` on a sub-resource removes that feature:

- `DELETE /api/collections/{name}/stream` disables streaming and clears `oldImage`.
- `DELETE /api/collections/{name}/ttl` drops the TTL index and clears `ttlAttribute`.
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
| POST   | `/api/collections/:name/stream`        | Enable streaming with `oldImage`.                |
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

- **Collection names are unique**. Enforced by a unique index on `config.collections.collectionName`.
- **A collection name is required** when creating a collection.
- **If `sortKey` is defined, `partitionKey` is required**.
- **`partitionKey` and `sortKey` cannot be the same field**.
- **Key field names are configurable and never hard-coded as `pk`/`sk`**. The code uses whatever names the operator provides.
- **`_id` is managed by MongoDB and is never derived from key fields**. Key fields are stored explicitly on documents.
- **Deletion protection is enabled by default** on collection creation. The create handler overwrites any caller-provided value with `true`.
- **A protected collection cannot be deleted**. `Manager.Delete` returns `ErrDeletionProtectionEnabled` unless protection is first disabled.
- **Stream configuration is immutable while enabled**. `EnableStream` returns `ErrStreamAlreadyExists` if `streamEnabled` is already `true`, regardless of the requested `oldImage` value.
- **Disabling a stream resets `streamEnabled` and `oldImage` to `false`**. This allows redefinition.
- **TTL configuration is immutable while set**. `SetTTL` returns `ErrTTLAlreadyExists` if `ttlAttribute` is already non-empty.
- **A sink belongs to exactly one collection**. Enforced by the `collectionId` reference and by scoping sink reads/deletes to that collection.
- **A sink can only be created if streaming is enabled** for its collection.
- **Sink event types, when specified, must be `INSERT`, `MODIFY`, or `REMOVE`**.
- **A sink must have a non-empty `type` and a non-empty `spec` object**. Type-specific required fields are validated by the sink implementation, not the shared model.
- **A watcher exists only for stream-enabled collections**. The manager starts watchers only for collections with `streamEnabled = true`.
- **There is at most one watcher per collection**. The manager's registry is keyed by collection name.
- **Resume tokens are isolated per collection**. Key format: `cdc:resume:{collectionName}`.
- **Resume tokens advance only after successful processing**. Failures route events to retry; the change stream cursor still advances, but the saved token reflects the last successfully handled event.
- **Delivery is at-least-once, not exactly-once.** A change event is delivered to the sinks at least once, but duplicates can occur: if the `MarkProcessed` idempotency write or the resume-token write fails after a successful dispatch, if Redis is unavailable, on crashes/restarts, or after downtime longer than the processed-key TTL. Downstream consumers must be idempotent using the deterministic `eventID`.
- **Idempotency is best-effort and bounded by the Redis processed-key TTL.** The processed key (`cdc:processed:{id}`) suppresses duplicate deliveries only within its TTL window (default `24h`, configurable via `PROCESSED_EVENT_TTL`). After it expires, a replayed event is delivered again.
- **A first-start checkpoint closes the enablement gap**. When a stream is enabled, `streamStartedAt` is recorded; a fresh watcher with no resume token anchors its stream at that checkpoint (`startAtOperationTime`), so events written between enablement and the first watcher start are streamed instead of skipped.
- **Resume tokens are never deleted on generic errors**. Transient failures (network, elections, cursor timeouts) preserve the token so the watcher resumes from the last successful position and skips nothing. The token is invalidated only when MongoDB explicitly rejects it as invalid (unparseable token, or a token from a dropped and recreated collection).
- **Idempotency is required for all event processing**. Duplicate event IDs within the processed-key TTL (default 24h, configurable via `PROCESSED_EVENT_TTL`) are skipped. Idempotency is best-effort and bounded by that TTL; delivery is at-least-once, so downstream consumers must be idempotent using `eventID`.
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
4. It starts a background sync loop that polls `config.collections` at `SyncInterval` (default 30 seconds, `DefaultConfig`).

## Synchronization

`syncWithCollections` runs on every tick:

1. Fetches stream-enabled collections from MongoDB.
2. Compares them with the current watcher registry.
3. Starts watchers for new collections.
4. For existing watchers:
   - If `oldImage` changed, restarts the watcher (because the change stream options must change).
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
- On a MongoDB error, the watcher deletes its resume token, waits 5 seconds, and loops. The next `watchOnce` call starts from the first-start checkpoint when one exists (resuming from enablement rather than "now"), otherwise from the latest position.
- If the collection is dropped or the change stream is invalidated, the watcher cancels itself and exits cleanly.

## Sink Refresh

`refreshSinks` loads the current sink specs from `config.sinks`, reconciles them against the previously registered set, and applies only the changes:

- New sinks are built and registered.
- Removed sinks are closed and unregistered.
- Existing sinks whose **mutable** config changed (`filter`, `eventTypes`) are updated **in place** via `dispatcher.Update`: the persisted config is swapped atomically inside the running `RuntimeSink` (an `atomic.Pointer` snapshot), so the transport is preserved and dispatch is not interrupted.

Sink identity is based on the persisted sink ID. `type`/`spec` are immutable for the sink's lifetime, but the mutable fields can change via PATCH; because each refresh reloads from `config.sinks`, a PATCHed sink converges on the next tick without restarting.

### How sink updates propagate

A mutable sink change flows end-to-end as:

1. **PATCH** `PATCH /api/collections/{name}/sinks/{id}` updates the mutable fields (`filter`, `eventTypes`) in `config.sinks` and fires `OnPublish`.
2. **Notify** — `OnPublish` publishes a config-change notification to Redis.
3. **Refresh** — the manager's `configChangeLoop` (or the periodic `syncLoop`) calls `refreshSinks`, which reloads the persisted sinks from `config.sinks`.
4. **Reconcile** — `ReconcileSinks` diffs the desired set against the current runtime set by sink ID; a mismatch in any mutable field emits `ChangeUpdated`.
5. **Apply** — `ApplyChanges` routes `ChangeUpdated` to `dispatcher.Update`, which finds the live `RuntimeSink` by identity and calls `UpdateConfig` on it. `UpdateConfig` builds a fresh immutable snapshot (normalized event types + filter) and stores it in the sink's `atomic.Pointer`. The transport is never closed or recreated, and the change stream is never restarted.

**What is applied live:** `filter` and `eventTypes` changes take effect atomically on the next refresh, without recreating the transport or interrupting dispatch. **What requires a new sink:** changing the immutable identity (`type`/`spec` — where events go) is rejected by PATCH (`400 sink_identity_immutable`); it requires delete + create, which rebuilds the transport by design. Sinks run whenever they exist (there is no `enabled` state); delivery is stopped by deleting the sink or disabling the stream.

## Resume Tokens

Resume tokens are fetched from Redis when a watcher starts and saved to Redis after each successfully processed event. When a watcher restarts because of an `oldImage` change or a transient error, it picks up the last token. If the token is invalid, the watcher clears it and resumes from the latest position.

**First-start resume-policy (priority, high → low)** — `Watcher.buildChangeStreamOptions` decides where each watch session opens the stream:

1. **Resume token** (when one exists): `resumeAfter` anchors the stream exactly at the last settled event (steady state; always wins).
2. **First-start checkpoint** (no token, but `streamStartedAt` present): `startAtOperationTime` anchors the stream at enablement, so every event from enablement is streamed — closing the enable → watcher-start gap and post-invalidate fresh sessions.
3. **Neither**: a fresh stream at the current position (legacy behavior for collections enabled long before the worker, or a token-invalidated restart with no checkpoint).

The checkpoint is captured at enablement from the API host clock (`primitive.Timestamp{T: unixNow, I: 1}`) — the pragmatic alternative to a causal-session `OperationTime`, documented as relying on approximate API-host/MongoDB clock agreement. After the first settled event, per-event token persistence in `processEvent` takes over and the checkpoint is never consulted again.

## Retry Registration

When a watcher is started, the manager calls `retryProcessor.RegisterCollection`. When a watcher is stopped, it calls `UnregisterCollection`. This tells the retry processor which queues to poll, preventing it from scanning queues for collections that no longer exist.

---

# Error Handling

## Domain Errors

`internal/collections/errors.go` defines sentinel errors for every predictable domain failure:

- `ErrCollectionNotFound`
- `ErrDocumentNotFound`
- `ErrSinkNotFound`
- `ErrSinkIdentityImmutable`
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
| `ErrSinkIdentityImmutable`                   | 400         | `sink_identity_immutable`     |
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
- **Resources are immutable whenever possible.** Stream, TTL, and protection sub-resources are immutable once set; changing them requires explicit deletion and recreation. Sinks are the exception: their _identity_ (`type`/`spec`) is immutable, but their _behavior_ (`filter`, `eventTypes`) is mutable via PATCH.
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

  **Graceful shutdown.** On SIGINT or SIGTERM the worker shuts down in dependency order, bounded by `SHUTDOWN_TIMEOUT` (default 30s): (1) the watcher manager is stopped first — cancelling its run context, waiting for its sync/config-change loops and every watcher, and closing pub/sub — so no new events flow while in-flight bookkeeping drains; (2) the retry processor is stopped, letting the current pass finish; (3) the dispatcher stops every sink lane (waiting for in-flight deliveries to drain) and closes the transports; (4) Redis is closed; (5) MongoDB is closed. Terminal bookkeeping writes (resume-token persist, `MarkProcessed`, retry `Enqueue`/`Remove`) and change-stream cursor close use a short detached context so a mid-flight event is never lost when the live context is cancelled. No arbitrary sleeps are used; shutdown is driven entirely by context cancellation and `sync.WaitGroup` waits. Panics in worker goroutines are contained: each long-running goroutine has a recover backstop that logs with a stack trace, per-event/per-tick work is panic-isolated so a single bad event cannot kill its loop, and a panicking watcher marks itself stopped for the manager's sync to reconcile.

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

- `dispatcher.go`: `Dispatcher` with concurrent-safe sink registry and parallel fan-out across per-sink lanes.
- `lane.go`: Per-sink execution lane — bounded job queue + worker pool; owns submission/backpressure and close/drain coordination.
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
