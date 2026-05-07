# Conduit

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-GPL--v3-blue.svg)](LICENSE)

MongoDB Change Data Capture (CDC) control plane + data plane system built in Go.

> **⚠️ Development Status**
> This project is under active development. APIs, configuration formats, and
> architecture decisions may change without prior notice. Use with caution in
> production environments.

## 📋 Overview

Conduit manages MongoDB collections and enables CDC (Change Data Capture) to external systems like HTTP endpoints, AWS EventBridge, or Meilisearch.

The project supports two operation modes:

- **DynamoDB-compatible mode**: collection defines `primary_key` and optional `sort_key`
- **MongoDB-native mode**: collection defines no key schema and uses default MongoDB behavior (`_id`)

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

## ✨ Features

- **Dual Compatibility**: DynamoDB-compatible key schema or pure MongoDB mode
- **DynamoDB-aligned Design**: logical key semantics, stream records, TTL fields
- **Per-collection Streaming**: Enable/disable CDC per collection configuration
- **Watcher Manager**: Centralized lifecycle management with no goroutine leaks
- **Resume Tokens**: Per-table resume positions stored in Redis
- **Idempotency**: Duplicate event prevention with TTL-based keys
- **Retry with Backoff**: Exponential backoff (1s → 5m), max 5 retries
- **Dead Letter Queue**: Failed events after max retries
- **Pluggable Destinations**: HTTP endpoints, EventBridge, Meilisearch, custom sinks

## 🏗 Architecture

### Core Components

| Component           | Package             | Description                                |
| ------------------- | ------------------- | ------------------------------------------ |
| **API**             | `cmd/api`           | REST control plane for collection management    |
| **Worker**          | `cmd/worker`        | CDC data plane with watchers               |
| **Watcher Manager** | `internal/watcher`  | Centralized watcher lifecycle + Pub/Sub    |
| **Dispatcher**      | `internal/dispatch` | Event routing to destinations              |
| **Retry Processor** | `internal/retry`    | Backoff and DLQ handling                   |
| **Redis Client**    | `internal/redis`    | State store + Pub/Sub operations           |
| **Mongo Client**    | `internal/mongo`    | Database + change streams + replica set    |
| **Collections Store**    | `internal/tables`   | Configuration management (`config.collections`) |

### Redis Key Structure

```
cdc:resume:<tableName>                 # Resume token per table
cdc:retry:<tableName>                  # Retry queue (sorted set)
cdc:dlq:<tableName>                    # Dead letter queue (list)
cdc:processed:<table>:<type>:<ts>      # Idempotency key (TTL: 24h)
cdc:config-change                      # Pub/Sub channel for table changes
```

**Pattern examples:**

- `cdc:processed:users:*` - all events for users table
- `cdc:processed:users:INSERT:*` - only INSERT events for users
- `cdc:processed:*:REMOVE:*` - all REMOVE events across tables

**Note:** Redis is used for state management (resume tokens, idempotency, retry, pub/sub). Events are sent to HTTP endpoints configured per table.

## 🚀 Quick Start

### Prerequisites

- Go 1.25+
- MongoDB 4.0+ (replica set required for change streams)
- Redis 6.0+
- Docker Compose (optional, for local development)

### 1. Clone and Setup

```bash
git clone <repository>
cd conduit
make init
```

### 2. Start Dependencies

**Option A: Full Stack (Recommended)**

Starts MongoDB, Redis, API, and Worker with a single command:

```bash
make docker-up-full
```

**Option B: Dependencies Only**

Starts only MongoDB and Redis (run API/Worker locally):

```bash
make docker-up
```

**Services:**
| Service | URL | Description |
|---------|-----|-------------|
| API | http://localhost:8080 | REST control plane |
| Worker | - | CDC data plane (background) |
| MongoDB | mongodb://localhost:27017 | Database |
| Redis | redis://localhost:6379 | State store |
| Mongo Express | http://localhost:8081 | MongoDB UI (admin/admin) |

**Note:** The API automatically initializes the MongoDB replica set on startup (required for change streams).

### 3. Configure Environment

**⚠️ Required Environment Variables**

The application **will not start** without these variables:

```bash
# Copy example and edit
cp .env.example .env
```

**Required configuration:**

```bash
# MongoDB (REQUIRED)
MONGODB_URI=mongodb://localhost:27017
MONGODB_DATABASE=conduit

# Redis - Use URI/DSN format (REQUIRED)
# Format: redis://[username[:password]@]host[:port][/db_number]
REDIS_URI=redis://localhost:6379

# Optional:
PORT=8080
```

**Redis URI Examples:**

```bash
# Simple (no auth)
REDIS_URI=redis://localhost:6379

# With password
REDIS_URI=redis://:mypassword@localhost:6379

# With username and password
REDIS_URI=redis://myuser:mypassword@localhost:6379

# With database number
REDIS_URI=redis://:password@localhost:6379/1

# TLS connection
REDIS_URI=rediss://:password@localhost:6380
```

### 4. Run Services (Local Development)

If you started only dependencies with `make docker-up`, run API and Worker locally:

```bash
# Terminal 1: Start API server
make run-api

# Terminal 2: Start Worker
make run-worker
```

The API will automatically initialize the MongoDB replica set on first start.

### 5. Create Your First Collection

```bash
curl -X POST http://localhost:8080/api/collections \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "users",
    "stream_enabled": true,
    "old_image": true,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "MODIFY", "REMOVE"]
      }
    ]
  }'
```

## 📖 API Reference

### Collections

| Method   | Endpoint            | Description     |
| -------- | ------------------- | --------------- |
| `GET`    | `/api/collections`       | List all collections |
| `POST`   | `/api/collections`       | Create collection    |
| `PUT`    | `/api/collections/:name` | Update collection    |
| `DELETE` | `/api/collections/:name` | Delete collection    |
| `GET`    | `/health`           | Health check    |

### Collection Schema

```json
{
  "collection_name": "users",
  "primary_key": "id",
  "sort_key": "email",
  "stream_enabled": true,
  "old_image": true,
  "ttl_attribute": "expiresAt",
  "deletion_protection": true,
  "destinations": [
    {
      "type": "http",
      "endpoint": "http://localhost:3000/events",
      "event_types": ["INSERT", "MODIFY", "DELETE"]
    }
  ]
}
```

**Collection Configuration:**

| Field                 | Type   | Default | Description                                 |
| --------------------- | ------ | ------- | ------------------------------------------- |
| `collection_name`          | string | -       | Name of the MongoDB collection              |
| `primary_key`         | string | -       | Optional partition key field name           |
| `sort_key`            | string | -       | Optional sort key field name                |
| `stream_enabled`      | bool   | false   | Enable CDC streaming for this collection         |
| `old_image`           | bool   | false   | Include old document state in change events |
| `ttl_attribute`       | string | -       | Field name for TTL expiration               |
| `deletion_protection` | bool   | true    | Prevent accidental deletion (default: true) |
| `destinations`        | array  | []      | List of event destinations                  |

Key schema rules:

- If `sort_key` is defined, `primary_key` is required
- `primary_key` and `sort_key` can use any user-defined field names
- If both are omitted, collection runs in MongoDB-native mode

**Destination Configuration:**

| Field             | Type     | Required | Description                                                 |
| ----------------- | -------- | -------- | ----------------------------------------------------------- |
| `type`            | string   | Yes      | Destination type: `http`, `eventbridge`, `meilisearch`      |
| `event_types`     | []string | No       | Events to send: `INSERT`, `MODIFY`, `REMOVE` (default: all) |
| `filter_criteria` | object   | No       | Per-destination filtering on `old_image` / `new_image`      |

**HTTP-specific fields:**

| Field          | Type   | Required | Description                              |
| -------------- | ------ | -------- | ---------------------------------------- |
| `endpoint`     | string | Yes      | HTTP endpoint URL                        |
| `bearer_token` | string | No       | Optional bearer token for authentication |

**EventBridge-specific fields:**

| Field            | Type   | Required | Description                             |
| ---------------- | ------ | -------- | --------------------------------------- |
| `region`         | string | Yes      | AWS region (e.g. `us-east-1`)           |
| `event_bus_name` | string | Yes      | EventBridge event bus name              |
| `source`         | string | No       | Event source (default: `conduit-mongodb`) |

**Meilisearch-specific fields:**

| Field          | Type   | Required | Description                                     |
| -------------- | ------ | -------- | ----------------------------------------------- |
| `endpoint`     | string | Yes      | Meilisearch host (e.g. `http://localhost:7700`) |
| `bearer_token` | string | No       | Meilisearch API key                             |
| `index_name`   | string | No       | Target index (default: `collection_name`)            |

**HTTP Request:**

- Method: `POST`
- Headers: `Content-Type: application/json`, `Authorization: Bearer <token>` (if configured)
- Body: `StreamRecord` JSON (contains `tableName`, `recordType`, `newImage`, `oldImage`, `timestamp`)

### Example Requests

**Create collection with streaming (HTTP destination):**

```bash
curl -X POST http://localhost:8080/api/collections \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "orders",
    "stream_enabled": true,
    "old_image": true,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "MODIFY", "REMOVE"]
      }
    ]
  }'
```

**Create collection with bearer token authentication:**

```bash
curl -X POST http://localhost:8080/api/collections \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "orders",
    "stream_enabled": true,
    "old_image": true,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "bearer_token": "my-secret-token",
        "event_types": ["INSERT", "MODIFY", "REMOVE"]
      }
    ]
  }'
```

**Create collection with multiple destinations:**

```bash
curl -X POST http://localhost:8080/api/collections \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "orders",
    "stream_enabled": true,
    "old_image": true,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "REMOVE"]
      },
      {
        "type": "http",
        "endpoint": "http://localhost:3001/audit",
        "event_types": ["MODIFY"]
      }
    ]
  }'
```

**Update collection:**

```bash
curl -X PUT http://localhost:8080/api/collections/orders \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "orders",
    "stream_enabled": true,
    "old_image": false,
    "ttl_attribute": "expiresAt",
    "deletion_protection": true,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "REMOVE"]
      }
    ]
  }'
```

**Disable deletion protection and delete collection:**

```bash
# First, disable deletion protection
curl -X PUT http://localhost:8080/api/collections/orders \
  -H "Content-Type: application/json" \
  -d '{
    "collection_name": "orders",
    "stream_enabled": true,
    "deletion_protection": false,
    "destinations": [
      {
        "type": "http",
        "endpoint": "http://localhost:3000/events",
        "event_types": ["INSERT", "MODIFY", "REMOVE"]
      }
    ]
  }'

# Then delete the collection
curl -X DELETE "http://localhost:8080/api/collections/orders"
```

**Delete collection with protection enabled (fails):**

```bash
curl -X DELETE "http://localhost:8080/api/collections/orders"
# Returns: 403 Forbidden - "Deletion protection is enabled. Disable it before deleting the collection."
```

**List collections:**

```bash
curl http://localhost:8080/api/collections | jq .
```

## ⚙️ Configuration

### Environment Variables

| Variable           | Required | Default | Description                        |
| ------------------ | -------- | ------- | ---------------------------------- |
| `MONGODB_URI`      | **Yes**  | -       | MongoDB connection string          |
| `MONGODB_DATABASE` | **Yes**  | -       | Database name                      |
| `REDIS_URI`        | **Yes**  | -       | Redis URI/DSN for state management |
| `PORT`             | No       | `8080`  | API server port                    |

**Note:** HTTP endpoints are configured per-table via the API, not via environment variables.

### Redis URI Format

```
redis://[username[:password]@]host[:port][/db_number]
```

| Example                            | Description          |
| ---------------------------------- | -------------------- |
| `redis://localhost:6379`           | No authentication    |
| `redis://:password@localhost:6379` | Password only        |
| `redis://user:pass@localhost:6379` | Username + password  |
| `redis://:pass@localhost:6379/1`   | With database number |
| `rediss://:pass@localhost:6380`    | TLS connection       |

### Missing Variables

If a required variable is not set, the application will exit with a fatal error:

```
FATAL: Required environment variable MONGODB_URI is not set
```

### DynamoDB Compatibility and MongoDB Mode

Conduit uses DynamoDB terminology internally, but physical key field names are user-defined.

Modes:

- DynamoDB-compatible mode: configure `primary_key` and optional `sort_key`
- MongoDB-native mode: no key schema, use default MongoDB `_id` workflows

Default DynamoDB-aligned names (optional):

| Concept     | Field Name  |
| ----------- | ----------- |
| Primary Key | `pk`        |
| Sort Key    | `sk`        |
| Table       | `table`     |
| Item        | `item`      |
| Stream      | `stream`    |
| TTL Field   | `expiresAt` |
| New Image   | `newImage`  |
| Old Image   | `oldImage`  |

### Key Schema Examples

DynamoDB-compatible mode:

```json
{
  "collection_name": "users",
  "primary_key": "id",
  "sort_key": "email"
}
```

Item example:

```json
{
  "id": "USER#1",
  "email": "EMAIL#test@gmail.com"
}
```

MongoDB-native mode:

```json
{
  "collection_name": "logs"
}
```

## 🔄 Stream Activation Rules

Streaming is **explicitly enabled per collection**:

| `stream_enabled` | Worker Behavior                      |
| ---------------- | ------------------------------------ |
| `false`          | ❌ No watcher created, collection ignored |
| `true`           | ✅ Watcher created, events processed |

### Watcher Lifecycle

1. **Initial Load**: Fetch all `stream_enabled=true` collections from `config.collections`
2. **Start Watchers**: One watcher per enabled collection
3. **Sync Loop**:
   - Polling: Diff with `config.collections` every 15min (fallback)
   - **Push notifications**: Immediate sync via Redis Pub/Sub when tables change
4. **Resume Tokens**: Updated after each successful batch
5. **Graceful Stop**: Context cancellation, no data loss

## 🔁 Retry Behavior

| Attempt | Delay | Action              |
| ------- | ----- | ------------------- |
| 1       | 1s    | First retry         |
| 2       | 2s    | Exponential backoff |
| 3       | 4s    | Exponential backoff |
| 4       | 8s    | Exponential backoff |
| 5       | 16s   | Final retry         |
| 6+      | —     | → DLQ               |

**Max Retries:** 5  
**Backoff:** Exponential (capped at 5 minutes)  
**After 5 failures:** Event sent to Dead Letter Queue

## 🧪 Testing

```bash
# Unit tests
make test

# Integration tests (requires MongoDB + Redis)
make test-integration

# With coverage
make test-coverage

# Watch mode (requires entr)
make test-watch
```

### Test Coverage

All critical paths covered:

- ✅ CDC event processing
- ✅ Retry with exponential backoff
- ✅ Idempotency checks
- ✅ Watcher lifecycle (start/stop)
- ✅ Resume token management
- ✅ DLQ handling

## 🛠 Makefile Commands

**Development:**

```bash
make help              # Show all commands
make init              # Initialize project
make build             # Build all packages
make run-api           # Run API server (local)
make run-worker        # Run Worker (local)
make fmt               # Format code
make lint              # Run linter
make clean             # Clean build artifacts
```

**Docker:**

```bash
make docker-up         # Start MongoDB + Redis only
make docker-up-full    # Start full stack (API + Worker + MongoDB + Redis)
make docker-build      # Build Docker images
make docker-down       # Stop all containers
make docker-logs       # View logs in real-time
make docker-status     # Show container status
make docker-clean      # Clean containers and volumes
```

**Testing:**

```bash
make test              # Run unit tests
make test-integration  # Run integration tests
make test-coverage     # Run tests with coverage report
```

## 📁 Project Structure

```
conduit/
├── cmd/
│   ├── api/              # Control plane API (Gin)
│   └── worker/           # Data plane CDC worker
├── internal/
│   ├── dispatch/         # Event dispatcher + destinations
│   ├── mongo/            # MongoDB client wrapper
│   ├── redis/            # Redis client wrapper
│   ├── retry/            # Retry processor with backoff
│   ├── streams/          # Change stream watcher
│   ├── tables/           # Collection configuration store
│   └── watcher/          # Watcher manager + lifecycle
├── examples/
│   ├── create_table.sh   # Example: Create collection
│   └── monitor_queues.sh # Example: Monitor queues
├── compose.yaml          # Docker Compose config
├── Makefile
├── .env.example          # Environment variables template
├── go.mod
├── AGENTS.md             # Development guidelines
└── README.md
```

## 📊 Monitoring

### Key Metrics

- Active watchers count
- Retry queue length per collection
- DLQ length per collection
- Events processed per second
- Last error time per watcher

### Monitor Queues

```bash
# Using the example script
./examples/monitor_queues.sh

# Or manually with Redis CLI (for retry and DLQ queues)
redis-cli KEYS "cdc:retry:*"
redis-cli KEYS "cdc:dlq:*"
```

**Note:** Events are sent to HTTP endpoints configured per collection. Monitor your HTTP service logs for incoming events.

## 🔒 Critical Guarantees

| Guarantee              | Implementation                 |
| ---------------------- | ------------------------------ |
| **No event loss**      | Retry queue + DLQ              |
| **No duplicates**      | Idempotency keys (24h TTL)     |
| **No goroutine leaks** | Proper watcher lifecycle       |
| **Per-collection resume**   | Individual tokens in Redis     |
| **Graceful shutdown**  | Context cancellation + timeout |

## 🚨 Troubleshooting

### Missing Environment Variables

```
FATAL: Required environment variable MONGODB_URI is not set
```

**Solution:** Set all required variables before running:

```bash
export MONGODB_URI=mongodb://localhost:27017
export MONGODB_DATABASE=conduit
export REDIS_URI=redis://localhost:6379
```

### Worker Not Detecting New Collections Immediately

New collections should be detected within ~1 second via Redis Pub/Sub. If not:

1. Check Redis is running: `redis-cli ping` → should return `PONG`
2. Check worker logs for Pub/Sub subscription: `docker logs conduit-mongodb-worker-1 | grep "config-change"`
3. Verify API is publishing: `docker logs conduit-mongodb-api-1 | grep "config change"`
4. Fallback: Sync runs every 30s if Pub/Sub fails

### Invalid Redis URI

```
FATAL: Failed to create worker: parse redis URI: invalid redis URL scheme:
```

**Solution:** Ensure URI starts with `redis://` or `rediss://`:

```bash
# Correct:
REDIS_URI=redis://localhost:6379

# Wrong:
REDIS_URI=localhost:6379
```

### Change Streams Not Working

The API automatically initializes the replica set on startup. If you're running MongoDB locally:

```bash
# Check replica set status
mongosh --eval "rs.status()"
```

If using Docker, the replica set is initialized automatically by the API on first start.

### Redis Connection Failed

```bash
# Check Redis is running
redis-cli -h localhost -p 6379 ping

# Should return: PONG
```

### Watcher Not Starting

1. Verify `stream_enabled: true` in collection config
2. Check MongoDB connection
3. Check Redis connection
4. Review worker logs for errors

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📝 License

GNU General Public License v3.0 - see [LICENSE](LICENSE.md) for details.
