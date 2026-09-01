package dispatch

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// sinkSnapshot is an immutable copy of a sink's persisted configuration,
// swapped atomically so Send evaluates against a consistent view without a
// lock.
type sinkSnapshot struct {
	sink       collections.Sink
	eventTypes map[string]bool
}

// RuntimeSink is the active, in-memory representation of a persisted sink.
// Transports receive only events that pass the sink's event types and filter.
type RuntimeSink struct {
	collections.Sink

	Transport Transport

	// snapshot holds the current filter/event-types set, swapped atomically
	// so Send and UpdateConfig are safe to race.
	snapshot atomic.Pointer[sinkSnapshot]
}

// NewRuntimeSink creates a runtime sink from its persisted configuration and
// an instantiated transport.
func NewRuntimeSink(sink collections.Sink, transport Transport) *RuntimeSink {
	rs := &RuntimeSink{
		Sink:      sink,
		Transport: transport,
	}
	rs.snapshot.Store(newSinkSnapshot(sink))
	return rs
}

// newSinkSnapshot builds a snapshot, normalizing event types to uppercase so
// evaluation is case-insensitive.
func newSinkSnapshot(sink collections.Sink) *sinkSnapshot {
	cfg := &sinkSnapshot{sink: sink}
	if len(sink.EventTypes) > 0 {
		cfg.eventTypes = make(map[string]bool, len(sink.EventTypes))
		for _, et := range sink.EventTypes {
			cfg.eventTypes[strings.ToUpper(et)] = true
		}
	}
	return cfg
}

// Key returns the sink's identity, used to address it in the dispatcher.
func (rs *RuntimeSink) Key() string {
	return rs.Sink.Identity()
}

// Send evaluates the sink's routing rules and, if the event should be
// delivered, delegates to the transport.
func (rs *RuntimeSink) Send(ctx context.Context, record streams.StreamRecord) error {
	cfg := rs.snapshot.Load()
	if !eventTypeAllowed(cfg.eventTypes, record.RecordType) {
		return nil
	}
	if !cfg.sink.Filter.Matches(record.NewImage, record.OldImage) {
		return nil
	}
	return rs.Transport.Send(ctx, record)
}

// UpdateConfig atomically swaps the sink's filter and event types,
// preserving the transport. Must be called under the dispatcher's mutex so
// the embedded Sink field (read by Key) is not raced. The lane keeps running
// across an update: only the snapshot is swapped, so in-flight jobs observe
// either the old or the new config, never a torn one.
func (rs *RuntimeSink) UpdateConfig(sink collections.Sink) {
	rs.Sink = sink
	rs.snapshot.Store(newSinkSnapshot(sink))
}

// Close closes the underlying transport.
func (rs *RuntimeSink) Close() error {
	if rs.Transport == nil {
		return nil
	}
	return rs.Transport.Close()
}

func eventTypeAllowed(eventTypes map[string]bool, rt streams.RecordType) bool {
	if len(eventTypes) == 0 {
		return true
	}
	return eventTypes[string(rt)]
}
