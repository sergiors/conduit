package dispatch

import (
	"context"

	"github.com/sergiors/conduit/internal/streams"
)

// Destination defines the interface for event destinations.
// Each implementation (HTTP, EventBridge, Meilisearch) provides its own
// configuration and send logic while conforming to this contract.
type Destination interface {
	Send(ctx context.Context, record streams.StreamRecord) error
	Close() error
	Name() string
}
