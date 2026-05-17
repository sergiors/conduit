# 🧾 CLAUDEs.md

## Project

This project is a control plane + data plane system built in Go.

It manages MongoDB collections and enables CDC (Change Data Capture)
to external systems like HTTP, EventBridge, Redis Streams, or Meilisearch.

Main components:

-   API (control plane)
-   Worker (data plane)
-   MongoDB (storage + change streams)
-   Redis (worker state store)

---

## Tech Stack

-   Language: Go
-   HTTP Framework: Gin
-   Validation: go-playground/validator
-   Database: MongoDB
-   State Store: Redis
-   Messaging: Redis Streams / EventBridge (pluggable)
-   Architecture: event-driven, streaming

---

## Core Concepts

### DynamoDB-aligned Design (IMPORTANT)

All data structures, APIs, and behaviors MUST follow DynamoDB concepts
as closely as possible.

Compatibility model:

-   DynamoDB-compatible mode: collection defines key schema (`primary_key` and optional `sort_key`)
-   MongoDB-native mode: collection defines no key schema and uses default MongoDB behavior (`_id`)

The system operates with DynamoDB semantics:

-   partition key
-   sort key
-   item
-   table
-   stream
-   new image
-   old image
-   TTL

However, physical field names are configurable per collection.

---

## Naming Conventions

The system uses DynamoDB terminology internally.

### Default Field Names

| Concept       | Default Name |
| ------------- | ------------ |
| Partition Key | `pk`         |
| Sort Key      | `sk`         |
| Table         | `table`      |
| Item          | `item`       |
| Stream        | `stream`     |
| TTL Field     | `expiresAt`  |
| New Image     | `newImage`   |
| Old Image     | `oldImage`   |

These field names are defaults only.

Tables MAY customize key field names.

---

## Keys (Partition Key + Sort Key)

The system supports DynamoDB-style composite keys.

### Configurable Key Fields (IMPORTANT)

Partition key and sort key field names MUST be configurable per collection.

Example:

```json
{
    "partitionKey": "id",
    "sortKey": "sort"
}
```

Stored document:

```json
{
    "id": "USER#1",
    "sort": "EMAIL#test@gmail.com"
}
```

Another example:

```json
{
    "partitionKey": "tenantId",
    "sortKey": "createdAt"
}
```

### Rules

-   Support collections with no key schema (MongoDB-native mode)
-   Support partition-key-only tables
-   Support partition+sort-key tables
-   `_id` is managed by MongoDB and MUST NOT be derived from key fields
-   Key fields MUST always be explicitly stored
-   APIs MUST NOT assume `pk` and `sk`
-   Query builders MUST use configured key field names
-   CDC logic MUST use configured key field names
-   Indexes MUST use configured key field names
-   Internal logic MUST operate on logical concepts rather than physical field names

### Indexing

If a collection defines key schema, the system MUST create indexes using configured key fields.
If a collection has no key schema, no DynamoDB-style key index is required.

Example:

```js
{ id: 1, sort: 1 }
```

---

## CDC Event Model

CDC payloads MUST expose logical key concepts instead of physical storage field names.

### Example

❌ Incorrect:

```json
{
    "pk": "USER#1",
    "sk": "EMAIL#test@gmail.com"
}
```

✅ Correct:

```json
{
    "keys": {
        "partitionKey": "USER#1",
        "sortKey": "EMAIL#test@gmail.com"
    }
}
```

This abstraction allows field customization without affecting downstream consumers.

---

## 🚨 Stream Activation Rules (CRITICAL)

Streaming (CDC) MUST be explicitly enabled per collection via the `stream_enabled` boolean field.

### Behavior

If:

```json
{
    "stream_enabled": false
}
```

Then:

-   Worker MUST NOT create watcher
-   Worker MUST ignore collection

If:

```json
{
    "stream_enabled": true
}
```

Then:

-   Worker MUST create watcher
-   Worker MUST process events
-   Worker MUST dispatch events

---

## 🔁 Watcher Lifecycle Management (CRITICAL)

### Requirements

-   One watcher per collection
-   No duplicate watchers
-   Watchers MUST be stoppable
-   No goroutine leaks
-   Watchers MUST be context-driven

---

## 🧠 Watcher Manager (CORE COMPONENT)

The worker MUST implement a centralized Watcher Manager responsible for:

-   Managing active watchers
-   Synchronizing with `config.collections`
-   Starting watchers dynamically
-   Stopping watchers dynamically
-   Restarting failed watchers safely

### Watcher Registry

```go
map[string]*Watcher
```

The Watcher Manager is the source of truth for active watchers.

---

## Resume Token Management (IMPORTANT)

Resume tokens MUST be isolated per collection.

### Redis Key Format

```txt
cdc:resume_token:<collectionName>
```

### Rules

-   Resume token MUST be per collection
-   Each watcher MUST manage its own resume token
-   Resume tokens MUST NOT be shared across collections
-   Resume token updates MUST happen only after successful processing
-   Failed events MUST NOT advance the resume token

---

## Watcher Lifecycle Flow

### Initial Load

1. Load collections from configuration
2. Start watchers for enabled collections

### Sync Loop

1. Fetch latest collection configuration
2. Diff desired state vs active watchers
3. Start missing watchers
4. Stop removed or disabled watchers

---

## Resume Failure Handling

If a resume token becomes invalid:

-   Restart stream from latest position
-   Log warning
-   Preserve worker availability
-   System MUST NOT crash

---

## Worker Responsibilities

Worker ONLY operates on collections with streaming enabled.

Responsibilities:

-   Read MongoDB change streams
-   Transform events
-   Dispatch events to sinks
-   Handle retries
-   Handle DLQ
-   Handle idempotency
-   Manage resume tokens and offsets

Worker MUST remain stateless except for Redis state.

---

## Redis Usage (Worker State)

### Resume Token

```txt
cdc:resume_token:<collectionName>
```

---

### Retry Queue

```txt
cdc:retry:<collectionName>
```

---

### Event Payload

```txt
cdc:event:<id>
```

---

### DLQ

```txt
cdc:dlq:<collectionName>
```

---

### Idempotency

```txt
cdc:processed:<collectionName>:<id>
```

---

## Retry Rules

-   Max retries: 5
-   Exponential backoff REQUIRED
-   After 5 failures → send to DLQ

---

## Critical Rules

### Resume Token

-   Only update resume token after successful processing
-   Never skip failed events
-   Never acknowledge partially processed events

### Idempotency

-   Required for all event processing

### Ordering

-   Ordering is NOT guaranteed
-   Consumers MUST support eventual consistency

---

## Backpressure

Worker MUST tolerate overload conditions.

Requirements:

-   Retry queue growth MUST be monitored
-   System MUST degrade gracefully
-   Worker MUST NOT crash under load
-   Memory usage MUST remain bounded

---

## Destinations

Supported sinks:

-   Redis Streams
-   EventBridge
-   HTTP Webhooks
-   Meilisearch

The destination architecture MUST remain pluggable.

---

## Code Style

-   Small packages
-   Explicit dependencies
-   No global mutable state
-   Use context everywhere
-   Explicit error handling
-   No panic in production
-   Prefer composition over inheritance-like abstractions
-   Favor simple concurrency patterns

---

## Testing Strategy

The system MUST include:

-   CDC tests
-   Retry tests
-   DLQ tests
-   Idempotency tests
-   Resume token recovery tests
-   Watcher lifecycle tests
-   Dynamic watcher synchronization tests
-   Backpressure tests

### Critical Guarantees

-   No event loss
-   No duplicate processing
-   No goroutine leaks
-   No duplicate watchers

---

## Git

-   Use conventional commits:
    -   `feat:`
    -   `fix:`
    -   `chore:`
    -   `test:`
    -   `docs:`
    -   `refactor:`
-   Do not add attribution trailers
-   Do not add generated-by messages
-   Prefer worktrees for parallel work to avoid conflicts

---

## Commands

### API

```bash
go run ./cmd/api
```

### Worker

```bash
go run ./cmd/worker
```

---

## Notes

-   Always read `config.collections`
-   Watcher Manager is the source of truth
-   Workers MUST dynamically reconcile watcher state
-   Streaming is opt-in per collection
-   Internal semantics MUST remain DynamoDB-aligned
-   The system MUST support configurable key field names
-   The system MUST never lose events
