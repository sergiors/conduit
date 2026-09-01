package watcher

import (
	"context"
	"fmt"
	"log"
	"reflect"

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
	ChangeUpdated ChangeType = "updated"
)

// Reconciliation holds the result of reconciling the desired sink state with
// the current runtime state.
type Reconciliation struct {
	Changes []SinkChange
}

// Summary returns a human-readable summary of changes.
func (r *Reconciliation) Summary() string {
	added, removed, updated := 0, 0, 0
	for _, c := range r.Changes {
		switch c.Type {
		case ChangeAdded:
			added++
		case ChangeRemoved:
			removed++
		case ChangeUpdated:
			updated++
		}
	}
	return fmt.Sprintf("%d added, %d removed, %d updated", added, removed, updated)
}

// ReconcileSinks compares the desired sinks with the current sinks by their
// unique IDs and returns the required changes.
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

	// Find additions and updates
	for id, des := range desiredByID {
		cur, exists := currentByID[id]
		if !exists {
			result.Changes = append(result.Changes, SinkChange{
				Type: ChangeAdded,
				Sink: des,
			})
			continue
		}
		if !mutableFieldsEqual(cur, des) {
			result.Changes = append(result.Changes, SinkChange{
				Type: ChangeUpdated,
				Sink: des,
			})
		}
	}

	return result
}

// mutableFieldsEqual reports whether two persisted sinks have the same
// mutable fields (filter, eventTypes). Type and spec are immutable identity.
func mutableFieldsEqual(a, b collections.Sink) bool {
	return reflect.DeepEqual(a.Filter, b.Filter) &&
		reflect.DeepEqual(a.EventTypes, b.EventTypes)
}

// ApplyChanges applies the reconciliation changes to a dispatcher.
func (r *Reconciliation) ApplyChanges(ctx context.Context, collectionName string, disp dispatcher) {
	for _, change := range r.Changes {
		switch change.Type {
		case ChangeRemoved:
			disp.Remove(collectionName, change.Sink.ID)
		case ChangeUpdated:
			// Update applies the change in place without rebuilding the
			// transport; fall back to a full register if the sink is not
			// registered (e.g. created while the watcher was stopped).
			if !disp.Update(collectionName, change.Sink) {
				transport := dispatch.BuildTransport(ctx, collectionName, change.Sink.Type, change.Sink.Spec)
				if transport == nil {
					log.Printf("Failed to build transport for updated sink type %s (collection %s, sink %s); skipping registration", change.Sink.Type, collectionName, change.Sink.ID)
					continue
				}
				sink := dispatch.NewRuntimeSink(change.Sink, transport)
				disp.Register(collectionName, sink)
			}
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
	Update(collection string, sink collections.Sink) bool
}

// LogChanges logs the changes at the appropriate level.
func (r *Reconciliation) LogChanges(collectionName string) {
	for _, change := range r.Changes {
		switch change.Type {
		case ChangeAdded:
			log.Printf("Added sink %s for collection %s", change.Sink.ID, collectionName)
		case ChangeRemoved:
			log.Printf("Removed sink %s for collection %s", change.Sink.ID, collectionName)
		case ChangeUpdated:
			log.Printf("Updated sink %s for collection %s", change.Sink.ID, collectionName)
		}
	}
}
