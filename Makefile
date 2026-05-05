.PHONY: build test test-unit test-integration clean run-api run-worker fmt lint vet help docker-up docker-down deps

GOCACHE := /tmp/go-build
GO := go
GOTEST := $(GO) test -v
BUILD_DIR := ./bin

all: fmt vet lint build test

build: ## Build all packages
	@echo "Building all packages..."
	GOCACHE=$(GOCACHE) $(GO) build -o $(BUILD_DIR)/ ./...

build-api: ## Build API server
	@echo "Building API server..."
	GOCACHE=$(GOCACHE) $(GO) build -o $(BUILD_DIR)/api ./cmd/api

build-worker: ## Build Worker
	@echo "Building Worker..."
	GOCACHE=$(GOCACHE) $(GO) build -o $(BUILD_DIR)/worker ./cmd/worker

build-all: build-api build-worker ## Build all binaries

test: test-unit ## Run unit tests

test-unit: ## Run unit tests (no integration)
	@echo "Running unit tests..."
	GOCACHE=$(GOCACHE) $(GOTEST) -short ./...

test-integration: ## Run integration tests (requires MongoDB + Redis)
	@echo "Running integration tests..."
	GOCACHE=$(GOCACHE) $(GOTEST) -tags=integration ./...

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	GOCACHE=$(GOCACHE) $(GOTEST) -short -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

run-api: ## Run API server locally
	@echo "Starting API server..."
	GOCACHE=$(GOCACHE) $(GO) run ./cmd/api

run-worker: ## Run Worker locally
	@echo "Starting Worker..."
	GOCACHE=$(GOCACHE) $(GO) run ./cmd/worker

fmt: ## Format Go code
	@echo "Formatting code..."
	$(GO) fmt ./...

vet: ## Run go vet
	@echo "Running go vet..."
	GOCACHE=$(GOCACHE) $(GO) vet ./...

lint: ## Run golangci-lint
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

check: fmt vet lint test ## Run all checks

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod tidy

docker-up: ## Start full stack with Docker (MongoDB + Redis + API + Worker)
	@echo "Starting full stack (MongoDB + Redis + API + Worker)..."
	docker compose up -d --build
	@echo "Waiting for services to be ready..."
	@sleep 5
	@echo ""
	@echo "Services:"
	@echo "  API:      http://localhost:8080"
	@echo "  MongoDB:  mongodb://localhost:27017"
	@echo "  Redis:    localhost:6379"
	@echo "  Mongo Express: http://localhost:8081 (admin/admin)"

docker-down: ## Stop all services
	@echo "Stopping all services..."
	docker compose down

docker-clean: ## Clean containers and volumes
	@echo "Cleaning containers and volumes..."
	docker compose down -v
	docker system prune -f

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf $(GOCACHE)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	$(GO) clean -cache -testcache

init: deps fmt vet ## Initialize project (first time setup)
	@echo "Project initialized!"

help: ## Show this help message
	@echo "Conduit MongoDB CDC - Makefile Commands"
	@echo "======================================"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
	@echo ""
