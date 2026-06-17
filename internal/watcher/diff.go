package watcher

import (
	"context"
	"fmt"
	"log"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
)

// SinkChange represents a change to a sink
type SinkChange struct {
	Type       ChangeType
	Name       string
	Config     collections.SinkConfig
	OldConfig  collections.SinkConfig // Only for updates
	ChangeDesc string                 // Human-readable description
}

// ChangeType represents the type of sink change
type ChangeType string

const (
	ChangeAdd    ChangeType = "add"
	ChangeRemove ChangeType = "remove"
	ChangeUpdate ChangeType = "update"
)

// DiffResult holds the result of comparing sink configurations
type DiffResult struct {
	Changes []SinkChange
}

// Summary returns a human-readable summary of changes
func (d *DiffResult) Summary() string {
	adds, removes, updates := 0, 0, 0
	for _, c := range d.Changes {
		switch c.Type {
		case ChangeAdd:
			adds++
		case ChangeRemove:
			removes++
		case ChangeUpdate:
			updates++
		}
	}
	return fmt.Sprintf("%d added, %d removed, %d updated", adds, removes, updates)
}

// DiffSinks compares current and desired sink configs
// and returns a structured diff result
func DiffSinks(current, desired []collections.SinkConfig) *DiffResult {
	currentByKey := make(map[string]collections.SinkConfig, len(current))
	for _, c := range current {
		currentByKey[sinkName(c)] = c
	}

	desiredByKey := make(map[string]collections.SinkConfig, len(desired))
	for _, d := range desired {
		desiredByKey[sinkName(d)] = d
	}

	result := &DiffResult{
		Changes: make([]SinkChange, 0),
	}

	// Find removals and updates
	for key, cur := range currentByKey {
		des, exists := desiredByKey[key]
		if !exists {
			result.Changes = append(result.Changes, SinkChange{
				Type:       ChangeRemove,
				Name:       key,
				Config:     cur,
				ChangeDesc: fmt.Sprintf("Remove sink %s", key),
			})
		} else if !configEqual(cur, des) {
			result.Changes = append(result.Changes, SinkChange{
				Type:       ChangeUpdate,
				Name:       key,
				Config:     des,
				OldConfig:  cur,
				ChangeDesc: describeChange(cur, des),
			})
		}
	}

	// Find additions
	for key, des := range desiredByKey {
		if _, exists := currentByKey[key]; !exists {
			result.Changes = append(result.Changes, SinkChange{
				Type:       ChangeAdd,
				Name:       key,
				Config:     des,
				ChangeDesc: fmt.Sprintf("Add sink %s", key),
			})
		}
	}

	return result
}

// describeChange returns a human-readable description of what changed
func describeChange(old, new collections.SinkConfig) string {
	changes := ""

	if old.Endpoint != new.Endpoint {
		changes += "endpoint, "
	}
	if old.BearerToken != new.BearerToken {
		changes += "token, "
	}
	if !eventTypesEqual(old.EventTypes, new.EventTypes) {
		changes += "event-types, "
	}
	if !filterEqual(old.FilterCriteria, new.FilterCriteria) {
		changes += "filter, "
	}
	if old.Region != new.Region {
		changes += "region, "
	}
	if old.EventBusName != new.EventBusName {
		changes += "event-bus, "
	}
	if old.IndexName != new.IndexName {
		changes += "index, "
	}

	if changes == "" {
		return "config"
	}
	return changes[:len(changes)-2] // trim trailing ", "
}

func eventTypesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSet := make(map[string]bool)
	for _, v := range a {
		aSet[v] = true
	}
	for _, v := range b {
		if !aSet[v] {
			return false
		}
	}
	return true
}

func filterEqual(a, b collections.FilterCriteria) bool {
	return imageFilterEqual(a.OldImage, b.OldImage) &&
		imageFilterEqual(a.NewImage, b.NewImage)
}

func imageFilterEqual(a, b collections.ImageFilter) bool {
	if len(a) != len(b) {
		return false
	}
	for field, condA := range a {
		condB, ok := b[field]
		if !ok {
			return false
		}
		if !filterConditionEqual(condA, condB) {
			return false
		}
	}
	return true
}

func filterConditionEqual(a, b collections.FilterCondition) bool {
	if !ptrStrEqual(a.Prefix, b.Prefix) || !ptrStrEqual(a.Suffix, b.Suffix) || !ptrBoolEqual(a.Exists, b.Exists) {
		return false
	}
	if !deepEqual(a.Numeric, b.Numeric) || !deepEqual(a.AnythingBut, b.AnythingBut) {
		return false
	}
	return true
}

func ptrStrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func ptrBoolEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b

}

func deepEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// ApplyChanges applies the diff result to a dispatcher
func (d *DiffResult) ApplyChanges(ctx context.Context, collectionName string, disp dispatcher) {
	for _, change := range d.Changes {
		switch change.Type {
		case ChangeRemove:
			disp.Remove(collectionName, change.Name)
		case ChangeAdd, ChangeUpdate:
			if created := dispatch.BuildSink(ctx, collectionName, change.Config); created != nil {
				if change.Type == ChangeUpdate {
					disp.Remove(collectionName, change.Name)
				}
				disp.Register(collectionName, created)
			}
		}
	}
}

// dispatcher is a minimal interface for applying sink changes
type dispatcher interface {
	Register(collection string, sink dispatch.Sink)
	Remove(collection, name string)
}

// LogChanges logs the changes at the appropriate level
func (d *DiffResult) LogChanges(collectionName string) {
	for _, change := range d.Changes {
		switch change.Type {
		case ChangeAdd:
			log.Printf("Added sink %s for collection %s", change.Name, collectionName)
		case ChangeRemove:
			log.Printf("Removed sink %s for collection %s", change.Name, collectionName)
		case ChangeUpdate:
			log.Printf("Updated sink %s for collection %s: %s", change.Name, collectionName, change.ChangeDesc)
		}
	}
}
