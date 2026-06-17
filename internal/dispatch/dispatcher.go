package dispatch

import (
	"context"
	"log"
	"sync"

	"github.com/sergiors/conduit/internal/streams"
)

// Dispatcher routes stream records to configured sinks.
type Dispatcher struct {
	sinks map[string][]Sink
	mu    sync.RWMutex
}

// NewDispatcher creates a new event dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		sinks: make(map[string][]Sink),
	}
}

// Register adds a sink for a collection.
func (d *Dispatcher) Register(collection string, sink Sink) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sinks[collection] == nil {
		d.sinks[collection] = make([]Sink, 0)
	}
	d.sinks[collection] = append(d.sinks[collection], sink)
}

// Dispatch sends a stream record to all configured sinks.
func (d *Dispatcher) Dispatch(ctx context.Context, collection string, record streams.StreamRecord) error {
	d.mu.RLock()
	sinks, ok := d.sinks[collection]
	d.mu.RUnlock()

	if !ok {
		return nil
	}

	var lastErr error
	for _, sink := range sinks {
		if err := sink.Send(ctx, record); err != nil {
			lastErr = err
			log.Printf("dispatch to %s failed: %v", sink.Name(), err)
		}
	}

	return lastErr
}

// Close all sinks.
func (d *Dispatcher) Close() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var lastErr error
	for _, sinks := range d.sinks {
		for _, sink := range sinks {
			if err := sink.Close(); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// Remove removes a single sink by name, closing it first.
func (d *Dispatcher) Remove(collection, name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	sinks, ok := d.sinks[collection]
	if !ok {
		return
	}

	for i, sink := range sinks {
		if sink.Name() == name {
			sink.Close()
			d.sinks[collection] = append(sinks[:i], sinks[i+1:]...)
			if len(d.sinks[collection]) == 0 {
				delete(d.sinks, collection)
			}
			return
		}
	}
}

// Clear removes all sinks for a collection (used when config changes).
func (d *Dispatcher) Clear(collection string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if sinks, ok := d.sinks[collection]; ok {
		for _, sink := range sinks {
			sink.Close()
		}
		delete(d.sinks, collection)
	}
}
