package dispatch

import (
	"context"
	"strings"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// RuntimeSink is the active, in-memory representation of a persisted sink.
// It glues the persisted configuration to a concrete Transport and is
// responsible for common sink behavior: event type filtering, filter criteria
// evaluation, and stable identity. Transports receive only events that
// should actually be delivered.
type RuntimeSink struct {
	collections.Sink

	Transport Transport

	eventTypes map[string]bool
}

// NewRuntimeSink creates a runtime sink from its persisted configuration and
// an instantiated transport.
func NewRuntimeSink(sink collections.Sink, transport Transport) *RuntimeSink {
	rs := &RuntimeSink{
		Sink:      sink,
		Transport: transport,
	}
	if len(sink.EventTypes) > 0 {
		rs.eventTypes = make(map[string]bool, len(sink.EventTypes))
		for _, et := range sink.EventTypes {
			rs.eventTypes[strings.ToUpper(et)] = true
		}
	}
	return rs
}

// Key returns a stable identifier derived from the persisted sink. It is the
// same value used when diffing sink configurations, so the dispatcher can be
// addressed by sink identity without involving the transport.
func (rs *RuntimeSink) Key() string {
	return rs.Sink.Identity()
}

// Send evaluates the sink's routing rules and, if the event should be
// delivered, delegates to the transport.
func (rs *RuntimeSink) Send(ctx context.Context, record streams.StreamRecord) error {
	if !rs.eventTypeAllowed(record.RecordType) {
		return nil
	}
	newMatch := collections.MatchImage(record.NewImage, rs.FilterCriteria.NewImage)
	oldMatch := collections.MatchImage(record.OldImage, rs.FilterCriteria.OldImage)
	if !newMatch || !oldMatch {
		return nil
	}
	return rs.Transport.Send(ctx, record)
}

// Close closes the underlying transport.
func (rs *RuntimeSink) Close() error {
	if rs.Transport == nil {
		return nil
	}
	return rs.Transport.Close()
}

func (rs *RuntimeSink) eventTypeAllowed(rt streams.RecordType) bool {
	if len(rs.eventTypes) == 0 {
		return true
	}
	return rs.eventTypes[string(rt)]
}
