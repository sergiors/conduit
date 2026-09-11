.PHONY: test test-unit test-integration test-coverage test-all fmt vet up down

GOCACHE := /tmp/go-build
GO := go
GOTEST := $(GO) test -v

test: test-unit

test-unit:
	GOCACHE=$(GOCACHE) $(GOTEST) -short ./...

test-integration:
	GOCACHE=$(GOCACHE) $(GOTEST) -tags=integration ./...

test-coverage:
	GOCACHE=$(GOCACHE) $(GOTEST) -short -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

test-all: test-unit test-integration

fmt:
	$(GO) fmt ./...

vet:
	GOCACHE=$(GOCACHE) $(GO) vet ./...

up:
	docker compose -f compose.dev.yaml up -d --build

down:
	docker compose -f compose.dev.yaml down
