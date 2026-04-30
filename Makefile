.PHONY: build test test-unit test-integration clean run-api run-worker fmt lint vet help docker-up docker-down deps

# Go variables
GOCACHE := /tmp/go-build
GO := go
GOFLAGS := -v
GOTEST := $(GO) test -v

# Build variables
BUILD_DIR := ./bin
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Default target
all: fmt vet lint build test

# ==============================================================================
# Build
# ==============================================================================

build: ## Build all packages
	@echo "Building all packages..."
	GOCACHE=$(GOCACHE) $(GO) build ./...

build-api: ## Build API server
	@echo "Building API server..."
	GOCACHE=$(GOCACHE) $(GO) build -o $(BUILD_DIR)/api ./cmd/api

build-worker: ## Build Worker
	@echo "Building Worker..."
	GOCACHE=$(GOCACHE) $(GO) build -o $(BUILD_DIR)/worker ./cmd/worker

build-all: build-api build-worker ## Build all binaries

# ==============================================================================
# Test
# ==============================================================================

test: test-unit ## Run all tests (alias for test-unit)

test-unit: ## Run unit tests (short mode, no integration)
	@echo "Running unit tests..."
	GOCACHE=$(GOCACHE) $(GOTEST) -short ./...

test-integration: ## Run integration tests (requires MongoDB + Redis)
	@echo "Running integration tests..."
	GOCACHE=$(GOCACHE) $(GOTEST) -tags=integration ./...

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	GOCACHE=$(GOCACHE) $(GOTEST) -short -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-watch: ## Run tests in watch mode (requires entr)
	@echo "Watching for changes..."
	find . -name '*.go' | entr -r $(MAKE) test-unit

# ==============================================================================
# Run
# ==============================================================================

run-api: ## Run API server (development)
	@echo "Starting API server..."
	GOCACHE=$(GOCACHE) $(GO) run ./cmd/api

run-worker: ## Run Worker (development)
	@echo "Starting Worker..."
	GOCACHE=$(GOCACHE) $(GO) run ./cmd/worker

run-all: ## Run both API and Worker (requires tmux or separate terminals)
	@echo "Use 'make run-api' and 'make run-worker' in separate terminals"

# ==============================================================================
# Code Quality
# ==============================================================================

fmt: ## Format Go code
	@echo "Formatting code..."
	$(GO) fmt ./...

vet: ## Run go vet
	@echo "Running go vet..."
	GOCACHE=$(GOCACHE) $(GO) vet ./...

lint: ## Run golangci-lint (if installed)
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
		echo "Install: brew install golangci-lint"; \
	fi

check: fmt vet lint test ## Run all checks

# ==============================================================================
# Dependencies
# ==============================================================================

deps: ## Download and tidy dependencies
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod tidy

deps-upgrade: ## Upgrade dependencies
	@echo "Upgrading dependencies..."
	$(GO) get -u ./...
	$(GO) mod tidy

deps-vendor: ## Vendor dependencies
	@echo "Vendoring dependencies..."
	$(GO) mod vendor

# ==============================================================================
# Docker
# ==============================================================================

docker-up: ## Start Docker Compose (MongoDB + Redis only)
	@echo "Starting Docker Compose..."
	docker compose up -d mongo redis mongo-express
	@echo "Waiting for services to be ready..."
	@sleep 3
	@echo "MongoDB: mongodb://localhost:27017"
	@echo "Redis:   localhost:6379"

docker-up-full: ## Start full stack (MongoDB + Redis + API + Worker)
	@echo "Starting full stack with Docker Compose..."
	docker compose up -d --build
	@echo "Waiting for services to be ready..."
	@sleep 5
	@echo ""
	@echo "Services started:"
	@echo "  API:      http://localhost:8080"
	@echo "  Worker:   Running in background"
	@echo "  MongoDB:  mongodb://localhost:27017"
	@echo "  Redis:    localhost:6379"
	@echo "  Mongo Express: http://localhost:8081 (admin/admin)"
	@echo ""
	@echo "Run 'make docker-logs' to view logs"

docker-down: ## Stop Docker Compose
	@echo "Stopping Docker Compose..."
	docker compose down

docker-build: ## Build Docker images
	@echo "Building Docker images..."
	docker compose build

docker-logs: ## Show Docker Compose logs
	docker compose logs -f

docker-status: ## Show Docker Compose status
	docker compose ps

docker-clean: ## Clean Docker volumes and containers

docker-logs: ## Show Docker Compose logs
	docker compose logs -f

docker-clean: ## Clean Docker volumes and containers
	@echo "Cleaning Docker..."
	docker compose down -v
	docker system prune -f

# ==============================================================================
# Clean
# ==============================================================================

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf $(GOCACHE)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	$(GO) clean -cache -testcache
	@echo "Clean complete"

clean-all: clean ## Clean everything including vendor
	rm -rf vendor/

# ==============================================================================
# Help
# ==============================================================================

help: ## Show this help message
	@echo "Relay MongoDB CDC - Makefile Commands"
	@echo "======================================"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""

# ==============================================================================
# Development
# ==============================================================================

init: deps fmt vet ## Initialize project (first time setup)
	@echo "Project initialized!"

dev: docker-up run-api ## Start development environment (API only)

dev-worker: docker-up run-worker ## Start development environment (Worker only)

# ==============================================================================
# Release
# ==============================================================================

release: check build-all ## Create release build
	@echo "Release build complete!"
	@ls -lh $(BUILD_DIR)/

.PHONY: all build build-api build-worker build-all test test-unit test-integration \
        test-coverage test-watch run-api run-worker run-all fmt vet lint check \
        deps deps-upgrade deps-vendor docker-up docker-down docker-logs docker-clean \
        clean clean-all help init dev dev-worker release
