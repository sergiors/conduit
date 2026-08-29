package watcher

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
)

// SinkChange represents a change produced by reconciling sink state.
type SinkChange struct {
	Type ChangeType
	Sink collections.Sink
}

// ChangeType represents the type of sink change produced by reconciliation.
type ChangeType string

const (
	ChangeAdded   ChangeType = "added"
	ChangeRemoved ChangeType = "removed"
)

// Reconciliation holds the result of reconciling the desired sink state with
// the current runtime state. Since sinks are immutable, only additions and
// removals can occur.
type Reconciliation struct {
	Changes []SinkChange
}

// Summary returns a human-readable summary of changes.
func (r *Reconciliation) Summary() string {
	added, removed := 0, 0
	for _, c := range r.Changes {
		switch c.Type {
		case ChangeAdded:
			added++
		case ChangeRemoved:
			removed++
		}
	}
	return fmt.Sprintf("%d added, %d removed", added, removed)
}

// ReconcileSinks compares the desired sinks with the current sinks by their
// unique IDs and returns the required changes. Sinks are immutable, so only
// additions and removals are produced.
func ReconcileSinks(current, desired []collections.Sink) *Reconciliation {
	currentByID := make(map[string]collections.Sink, len(current))
	for _, c := range current {
		currentByID[c.ID] = c
	}

	desiredByID := make(map[string]collections.Sink, len(desired))
	for _, d := range desired {
		desiredByID[d.ID] = d
	}

	result := &Reconciliation{
		Changes: make([]SinkChange, 0),
	}

	// Find removals
	for id, cur := range currentByID {
		if _, exists := desiredByID[id]; !exists {
			result.Changes = append(result.Changes, SinkChange{
				Type: ChangeRemoved,
				Sink: cur,
			})
		}
	}

	// Find additions
	for id, des := range desiredByID {
		if _, exists := currentByID[id]; !exists {
			result.Changes = append(result.Changes, SinkChange{
				Type: ChangeAdded,
				Sink: des,
			})
		}
	}

	return result
}

// ApplyChanges applies the reconciliation changes to a dispatcher.
func (r *Reconciliation) ApplyChanges(ctx context.Context, collectionName string, disp dispatcher) {
	for _, change := range r.Changes {
		switch change.Type {
		case ChangeRemoved:
			disp.Remove(collectionName, change.Sink.ID)
		case ChangeAdded:
			transport := dispatch.BuildTransport(ctx, collectionName, change.Sink.Type, change.Sink.Spec)
			if transport == nil {
				log.Printf("Failed to build transport for added sink type %s (collection %s, sink %s); skipping registration", change.Sink.Type, collectionName, change.Sink.ID)
				continue
			}
			sink := dispatch.NewRuntimeSink(change.Sink, transport)
			disp.Register(collectionName, sink)
		}
	}
}

// dispatcher is a minimal interface for applying sink changes.
type dispatcher interface {
	Register(collection string, sink *dispatch.RuntimeSink)
	Remove(collection, id string)
}

// LogChanges logs the changes at the appropriate level.
func (r *Reconciliation) LogChanges(collectionName string) {
	for _, change := range r.Changes {
		switch change.Type {
		case ChangeAdded:
			log.Printf("Added sink %s for collection %s", change.Sink.ID, collectionName)
		case ChangeRemoved:
			log.Printf("Removed sink %s for collection %s", change.Sink.ID, collectionName)
		}
	}
}
