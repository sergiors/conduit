package dispatch

import (
	"context"
	"log"
	"sync"

	"github.com/sergiors/conduit/internal/streams"
)

// Dispatcher routes stream records to the runtime sinks configured for each
// collection. Each RuntimeSink decides whether an event should be delivered
// before invoking its underlying Transport.
type Dispatcher struct {
	sinks map[string][]*RuntimeSink
	mu    sync.RWMutex
}

// NewDispatcher creates a new event dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		sinks: make(map[string][]*RuntimeSink),
	}
}

// Register adds a runtime sink for a collection.
func (d *Dispatcher) Register(collection string, sink *RuntimeSink) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sinks[collection] == nil {
		d.sinks[collection] = make([]*RuntimeSink, 0)
	}
	d.sinks[collection] = append(d.sinks[collection], sink)
}

// Dispatch sends a stream record to all runtime sinks for a collection.
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
			log.Printf("dispatch to %s failed: %v", sink.Key(), err)
		}
	}

	return lastErr
}

// Close closes all runtime sinks (and their transports).
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

// Remove removes a single runtime sink by its stable key, closing it first.
func (d *Dispatcher) Remove(collection, key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	sinks, ok := d.sinks[collection]
	if !ok {
		return
	}

	for i, sink := range sinks {
		if sink.Key() == key {
			sink.Close()
			d.sinks[collection] = append(sinks[:i], sinks[i+1:]...)
			if len(d.sinks[collection]) == 0 {
				delete(d.sinks, collection)
			}
			return
		}
	}
}

// Clear removes and closes all runtime sinks for a collection (used when
// config changes).
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
