# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git for fetching dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build API and Worker
RUN go build -o bin/api ./cmd/api
RUN go build -o bin/worker ./cmd/worker

# Runtime stage
FROM alpine:3.21

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates tzdata

# Copy binaries from builder
COPY --from=builder /app/bin/api ./bin/api
COPY --from=builder /app/bin/worker ./bin/worker

EXPOSE 8080

CMD ["./bin/api"]
