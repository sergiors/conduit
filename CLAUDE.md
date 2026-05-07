# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make test              # Run unit tests
make test-integration  # Run integration tests (requires MongoDB + Redis)
make test-coverage     # Run tests with coverage report
make build             # Build all packages
make run-api           # Run API server locally
make run-worker        # Run Worker locally
make docker-up         # Start MongoDB + Redis only
make docker-up-full    # Start full stack (API + Worker + MongoDB + Redis)
```

Run a single test:
```bash
go test -v -run TestName ./path/to/package
```

## Architecture

Conduit is a MongoDB Change Data Capture (CDC) system with control plane + data plane architecture:

```
┌─────────────────┐     ┌──────────────┐     ┌─────────────────┐
│   API Server    │────▶│   MongoDB    │────▶│    Worker       │
│   (Control)     │     │  (Storage)   │     │    (Data)       │
│   Port: 8080    │     │  Port: 27017 │     │                 │
└─────────────────┘     └──────────────┘     └─────────────────┘
                               │                    │
                               │ Change Streams     │ Redis State
                               ▼                    ▼
                        ┌──────────────┐     ┌─────────────┐
                        │  Watcher     │     │   Redis     │
                        │  Manager     │     │  Port: 6379 │
                        └──────────────┘     └─────────────┘
```

### Components

| Component | Package | Description |
|-----------|---------|-------------|
| API | `cmd/api` | REST control plane for collection management (Gin) |
| Worker | `cmd/worker` | CDC data plane with watchers |
| Watcher Manager | `internal/watcher` | Centralized watcher lifecycle + Pub/Sub |
| Dispatcher | `internal/dispatch` | Event routing to destinations (HTTP, EventBridge, Meilisearch) |
| Retry Processor | `internal/retry` | Exponential backoff and DLQ handling |
| Collections Store | `internal/collections` | Collection configuration in MongoDB |
| Redis Client | `internal/redis` | State store (resume tokens, idempotency, retry queues) |
| Mongo Client | `internal/mongo` | Database + change streams |

### Data Flow

1. API manages collection configurations in `config.collections`
2. Worker watches enabled collections via MongoDB change streams
3. Events are dispatched to configured destinations
4. Failed events go to retry queue with exponential backoff (1s → 5m, max 5 retries)
5. After max retries, events go to Dead Letter Queue (DLQ)

### Redis Keys

```
cdc:resume:<collectionName>          # Resume token per collection
cdc:retry:<collectionName>           # Retry queue (sorted set)
cdc:dlq:<collectionName>             # Dead letter queue (list)
cdc:processed:<id>                   # Idempotency key (TTL: 24h)
cdc:config-change                    # Pub/Sub channel for config changes
```

### Key Conventions

- Use `collection` terminology (not `table`) throughout the codebase
- Exception: `streams.StreamRecord` uses `TableName` field (DynamoDB-compatible)
- Streaming is opt-in per collection via `stream_specification.enabled`
- Watchers are managed centrally by `WatcherManager` with no goroutine leaks
- Resume tokens are per-collection and updated only after successful processing
