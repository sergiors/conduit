# Relay MongoDB CDC

[![Go Version](https://img.shields.io/badge/go-1.25+-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-GPL v3.0-green.svg)](LICENSE)

MongoDB Change Data Capture (CDC) control plane + data plane system built in Go.

## 📋 Overview

Relay MongoDB CDC manages MongoDB collections ("tables") and enables CDC (Change Data Capture) to external systems like Redis Streams or AWS EventBridge. The design follows **DynamoDB concepts** and naming conventions.

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

- **DynamoDB-aligned Design**: PK/SK patterns, stream records, TTL fields
- **Per-table Streaming**: Enable/disable CDC per table configuration
- **Watcher Manager**: Centralized lifecycle management with no goroutine leaks
- **Resume Tokens**: Per-table resume positions stored in Redis
- **Idempotency**: Duplicate event prevention with TTL-based keys
- **Retry with Backoff**: Exponential backoff (1s → 5m), max 5 retries
- **Dead Letter Queue**: Failed events after max retries
- **Pluggable Destinations**: Redis Streams, EventBridge, custom sinks

## 🏗 Architecture

### Core Components

| Component | Package | Description |
|-----------|---------|-------------|
| **API** | `cmd/api` | REST control plane for table management |
| **Worker** | `cmd/worker` | CDC data plane with watchers |
| **Watcher Manager** | `internal/watcher` | Centralized watcher lifecycle |
| **Dispatcher** | `internal/dispatch` | Event routing to destinations |
| **Retry Processor** | `internal/retry` | Backoff and DLQ handling |
| **Redis Client** | `internal/redis` | State store operations |
| **Mongo Client** | `internal/mongo` | Database + change streams |
| **Tables Store** | `internal/tables` | Configuration management |

### Redis Key Structure

```
cdc:resume_token:<tableName>     # Resume token per table
cdc:retry:<tableName>            # Retry queue (sorted set)
cdc:dlq:<tableName>              # Dead letter queue (list)
cdc:processed:<tableName>:<id>   # Idempotency key (TTL: 24h)
cdc:event:<id>                   # Event payload storage
cdc:events:<tableName>           # Redis Streams output
```

## 🚀 Quick Start

### Prerequisites

- Go 1.25+
- MongoDB 4.0+ (replica set required for change streams)
- Redis 6.0+
- Docker Compose (optional, for local development)

### 1. Clone and Setup

```bash
git clone <repository>
cd relay
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
MONGODB_DATABASE=relay

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

### 5. Create Your First Table

```bash
curl -X POST http://localhost:8080/tables \
  -H "Content-Type: application/json" \
  -d '{
    "table_name": "users",
    "stream_enabled": true,
    "old_image": true,
    "destinations": ["redis"]
  }'
```

## 📖 API Reference

### Tables

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/tables` | List all tables |
| `POST` | `/tables` | Create table |
| `PUT` | `/tables` | Update table |
| `DELETE` | `/tables?name=<name>` | Delete table |
| `GET` | `/health` | Health check |

### Table Schema

```json
{
  "table_name": "users",
  "stream_enabled": true,
  "old_image": true,
  "ttl_field": "expiresAt",
  "destinations": ["redis", "eventbridge"]
}
```

### Example Requests

**Create table with streaming:**
```bash
curl -X POST http://localhost:8080/tables \
  -H "Content-Type: application/json" \
  -d '{
    "table_name": "orders",
    "stream_enabled": true,
    "old_image": true,
    "destinations": ["redis"]
  }'
```

**Disable streaming:**
```bash
curl -X PUT http://localhost:8080/tables \
  -H "Content-Type: application/json" \
  -d '{
    "table_name": "orders",
    "stream_enabled": false
  }'
```

**List tables:**
```bash
curl http://localhost:8080/tables | jq .
```

## ⚙️ Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `MONGODB_URI` | **Yes** | - | MongoDB connection string |
| `MONGODB_DATABASE` | **Yes** | - | Database name |
| `REDIS_URI` | **Yes** | - | Redis URI/DSN (see examples below) |
| `PORT` | No | `8080` | API server port |

### Redis URI Format

```
redis://[username[:password]@]host[:port][/db_number]
```

| Example | Description |
|---------|-------------|
| `redis://localhost:6379` | No authentication |
| `redis://:password@localhost:6379` | Password only |
| `redis://user:pass@localhost:6379` | Username + password |
| `redis://:pass@localhost:6379/1` | With database number |
| `rediss://:pass@localhost:6380` | TLS connection |

### Missing Variables

If a required variable is not set, the application will exit with a fatal error:

```
FATAL: Required environment variable MONGODB_URI is not set
```

### DynamoDB Naming Conventions

| Concept | Field Name |
|---------|------------|
| Primary Key | `pk` |
| Sort Key | `sk` |
| Table | `table` |
| Item | `item` |
| Stream | `stream` |
| TTL Field | `expiresAt` |
| New Image | `newImage` |
| Old Image | `oldImage` |

### Key Format Example

```json
{
  "pk": "USER#1",
  "sk": "EMAIL#test@gmail.com"
}
```

## 🔄 Stream Activation Rules

Streaming is **explicitly enabled per table**:

| `stream_enabled` | Worker Behavior |
|------------------|-----------------|
| `false` | ❌ No watcher created, table ignored |
| `true` | ✅ Watcher created, events processed |

### Watcher Lifecycle

1. **Initial Load**: Fetch all `stream_enabled=true` tables
2. **Start Watchers**: One watcher per enabled table
3. **Sync Loop**: Diff with `system.tables` every 30s
4. **Resume Tokens**: Updated after each successful batch
5. **Graceful Stop**: Context cancellation, no data loss

## 🔁 Retry Behavior

| Attempt | Delay | Action |
|---------|-------|--------|
| 1 | 1s | First retry |
| 2 | 2s | Exponential backoff |
| 3 | 4s | Exponential backoff |
| 4 | 8s | Exponential backoff |
| 5 | 16s | Final retry |
| 6+ | — | → DLQ |

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
relay/
├── cmd/
│   ├── api/              # Control plane API (Gin)
│   └── worker/           # Data plane CDC worker
├── internal/
│   ├── dispatch/         # Event dispatcher + destinations
│   ├── mongo/            # MongoDB client wrapper
│   ├── redis/            # Redis client wrapper
│   ├── retry/            # Retry processor with backoff
│   ├── streams/          # Change stream watcher
│   ├── tables/           # Table configuration store
│   └── watcher/          # Watcher manager + lifecycle
├── examples/
│   ├── create_table.sh   # Example: Create table
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
- Retry queue length per table
- DLQ length per table
- Events processed per second
- Last error time per watcher

### Monitor Queues

```bash
# Using the example script
./examples/monitor_queues.sh

# Or manually with Redis CLI
redis-cli KEYS "cdc:retry:*"
redis-cli KEYS "cdc:dlq:*"
```

## 🔒 Critical Guarantees

| Guarantee | Implementation |
|-----------|----------------|
| **No event loss** | Retry queue + DLQ |
| **No duplicates** | Idempotency keys (24h TTL) |
| **No goroutine leaks** | Proper watcher lifecycle |
| **Per-table resume** | Individual tokens in Redis |
| **Graceful shutdown** | Context cancellation + timeout |

## 🚨 Troubleshooting

### Missing Environment Variables

```
FATAL: Required environment variable MONGODB_URI is not set
```

**Solution:** Set all required variables before running:
```bash
export MONGODB_URI=mongodb://localhost:27017
export MONGODB_DATABASE=relay
export REDIS_URI=redis://localhost:6379
```

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

1. Verify `stream_enabled: true` in table config
2. Check MongoDB connection
3. Check Redis connection
4. Review worker logs for errors

## 📝 License

GPL v3.0 License - see [LICENSE](LICENSE) for details.

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit changes (`git commit -m 'Add amazing feature'`)
4. Push to branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

---

**Built with ❤️ using Go and MongoDB**
