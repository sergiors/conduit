# 🧾 AGENTS.md

## Project

This project is a control plane + data plane system built in Go.  
It manages MongoDB collections ("tables") and enables CDC (Change Data Capture)
to external systems like HTTP, EventBridge or Meilisearch.

Main components:

- API (control plane)
- Worker (data plane)
- MongoDB (storage + change streams)
- Redis (state store for worker)

---

## Tech Stack

- Language: Go
- HTTP Framework: Gin
- Validation: go-playground/validator
- Database: MongoDB
- State Store: Redis
- Messaging: Redis Streams / EventBridge (pluggable)
- Architecture: event-driven, streaming

---

## Core Concepts

### DynamoDB-aligned Design (IMPORTANT)

All data structures, naming, and APIs MUST follow DynamoDB concepts as closely as possible.

---

### Naming Conventions

| Concept     | Use This Name |
| ----------- | ------------- |
| Primary Key | `pk`          |
| Sort Key    | `sk`          |
| Table       | `table`       |
| Item        | `item`        |
| Stream      | `stream`      |
| TTL field   | `expiresAt`   |
| New image   | `newImage`    |
| Old image   | `oldImage`    |

---

### Keys (PK + SK)

{
"pk": "USER#1",
"sk": "EMAIL#test@gmail.com"
}

Rules:

- Support pk-only and pk+sk
- `_id` may be generated as `pk#sk`
- Always store pk and sk explicitly
- Always create index `{ pk: 1, sk: 1 }`

---

## 🚨 Stream Activation Rules (CRITICAL)

Streaming (CDC) MUST be explicitly enabled per table.

### Behavior

- If `streamSpecification.enabled = false`:

  - Worker MUST NOT create watcher
  - Worker MUST ignore table

- If `streamSpecification.enabled = true`:
  - Worker MUST create watcher
  - Worker MUST process events
  - Worker MUST dispatch events

---

## 🔁 Watcher Lifecycle Management (CRITICAL)

### Requirements

- One watcher per table
- No duplicates
- Must be stoppable
- No goroutine leaks

---

## 🧠 Watcher Manager (CORE COMPONENT)

The worker MUST implement a centralized Watcher Manager responsible for:

- Managing active watchers
- Synchronizing with `config.tables`
- Starting/stopping watchers dynamically

### Watcher Registry

map[string]\*Watcher

### Resume Token (IMPORTANT)

- Resume token MUST be per table
- Key format:

  cdc:resume_token:<tableName>

- Each watcher MUST manage its own resume token
- Resume tokens MUST NOT be shared across tables

---

### Lifecycle Flow

Initial Load:

1. Load tables
2. Start watcher for each enabled table

Sync Loop:

1. Fetch tables
2. Diff with current watchers
3. Start/stop accordingly

---

### Resume Failure Handling

If resume token becomes invalid:

- Restart stream from latest position
- Log warning
- System MUST NOT crash

---

## Worker Responsibilities

Worker ONLY operates on tables with streaming enabled.

Responsibilities:

- Read change streams
- Transform events
- Dispatch to sinks
- Handle retry, DLQ, offsets

Worker MUST be stateless except for Redis.

---

## Redis Usage (Worker State)

### Resume Token

cdc:resume_token:<tableName>

---

### Retry Queue

cdc:retry:<tableName>

---

### Event Payload

cdc:event:<id>

---

### DLQ

cdc:dlq:<tableName>

---

### Idempotency

cdc:processed:<tableName>:<id>

---

## Retry Rules

- Max retries: 5
- Exponential backoff
- After 5 → DLQ

---

## Critical Rules

### Resume Token

- Only update after success
- Never skip failed events

### Idempotency

- Required for all processing

### Ordering

- Not guaranteed
- Must support eventual consistency

---

## Backpressure

Worker MUST handle overload:

- Retry queue growth must be monitored
- System MUST not crash under load

---

## Destinations

- Redis Streams
- EventBridge

---

## Code Style

- Small packages
- No global state
- Use context everywhere
- Explicit error handling
- No panic in production

---

## Testing Strategy

System MUST include:

- CDC tests
- Retry tests
- Idempotency tests
- Watcher lifecycle tests

### Critical

- No event loss
- No duplicate processing
- No goroutine leaks

---

## Git

- Use conventional commits: `feat:`, `fix:`, `chore:`, `test:`, `docs:`, `refactor:`.
- Do not add attribution trailers such as `Co-Authored-By` or generated-by messages.
- Prefer worktrees for parallel work to avoid conflicts.

---

## Commands

go run ./cmd/api  
go run ./cmd/worker

---

## Notes

- Always read `config.tables`
- Watcher Manager is the source of truth
- System MUST never lose events
