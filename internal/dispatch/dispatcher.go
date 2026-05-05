package dispatch

import (
	"context"
	"log"
	"sync"

	"github.com/sergiors/conduit/internal/streams"
)

// Dispatcher routes stream records to configured destinations.
type Dispatcher struct {
	destinations map[string][]Destination
	mu           sync.RWMutex
}

// NewDispatcher creates a new event dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		destinations: make(map[string][]Destination),
	}
}

// Register adds a destination for a table.
func (d *Dispatcher) Register(table string, dest Destination) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.destinations[table] == nil {
		d.destinations[table] = make([]Destination, 0)
	}
	d.destinations[table] = append(d.destinations[table], dest)
}

// Dispatch sends a stream record to all configured destinations.
func (d *Dispatcher) Dispatch(ctx context.Context, table string, record streams.StreamRecord) error {
	d.mu.RLock()
	dests, ok := d.destinations[table]
	d.mu.RUnlock()

	if !ok {
		return nil
	}

	var lastErr error
	for _, dest := range dests {
		if err := dest.Send(ctx, record); err != nil {
			lastErr = err
			log.Printf("dispatch to %s failed: %v", dest.Name(), err)
		}
	}

	return lastErr
}

// Close all destinations.
func (d *Dispatcher) Close() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var lastErr error
	for _, dests := range d.destinations {
		for _, dest := range dests {
			if err := dest.Close(); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// Remove removes a single destination by name, closing it first.
func (d *Dispatcher) Remove(table, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	dests, ok := d.destinations[table]
	if !ok {
		return
	}

	for i, dest := range dests {
		if dest.Name() == name {
			dest.Close()
			d.destinations[table] = append(dests[:i], dests[i+1:]...)
			if len(d.destinations[table]) == 0 {
				delete(d.destinations, table)
			}
			return
		}
	}
}

// Clear removes all destinations for a table (used when config changes).
func (d *Dispatcher) Clear(table string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if dests, ok := d.destinations[table]; ok {
		for _, dest := range dests {
			dest.Close()
		}
		delete(d.destinations, table)
	}
}
