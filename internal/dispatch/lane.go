package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"conduit/internal/streams"
)

// errLaneClosed is returned when a job is submitted to a lane that has been
// closed (e.g. during Remove/Clear/Close while a Dispatch is in flight). The
// caller treats it exactly like any other per-sink delivery failure: the event
// is not considered settled for that sink and the watcher falls back to the
// retry queue.
var errLaneClosed = errors.New("sink lane is closed")

// job is a single delivery request submitted to a sink lane. Workers pop jobs
// off the lane's bounded queue, call RuntimeSink.Send, and report the outcome
// on done. done is a shared buffered result channel sized to the number of
// lanes participating in a Dispatch, so a worker never blocks emitting.
type job struct {
	ctx    context.Context
	record streams.StreamRecord
	done   chan<- error
}

// lane is the per-sink execution lane owned by the dispatcher: a bounded job
// queue consumed by a small worker pool. Each worker calls the runtime sink's
// Send, so delivery for a sink is serialized only by that sink's own pool and
// is fully isolated from every other sink's lane — a slow or blocked sink
// cannot hold up delivery to the others.
//
// Close coordination uses a two-phase protocol with two separate channels:
//
//   - reject is closed first to unblock/reject new or backpressured submits.
//     Submitters select on reject (not on the worker-exit signal), so a submit
//     that wins the send race after close starts is still accepted and will be
//     drained, because workers are not told to exit until after every in-flight
//     submit has settled.
//   - stop is closed only after submitWG.Wait() returns, telling the workers to
//     stop normal service and drain whatever remains in the queue before
//     exiting. Because it is closed strictly after all racing submitters have
//     either enqueued or been rejected, no accepted job can be orphaned.
//
// stateMu only guards the closed flag and the in-flight submit count; it is
// never held across the potentially blocking enqueue. A submit checks closed
// under stateMu, registers itself in submitWG, then releases stateMu before
// blocking on the queue. close flips closed under stateMu, closes reject
// (which unblocks any backpressured submit with errLaneClosed), waits for every
// in-flight submit to settle via submitWG, and only then closes stop so the
// workers drain the queue and exit. This guarantees a job accepted while the
// lane is open is never orphaned (the drain loop executes whatever remains in
// the queue before the workers exit) and that close never deadlocks against a
// submit blocked on a full queue. The jobs channel itself is never closed, so a
// send can never panic; closure is signalled through the reject and stop
// channels instead.
type lane struct {
	sink *RuntimeSink
	jobs chan job
	// reject unblocks/rejects new or backpressured submits once close begins.
	reject chan struct{}
	// stop tells workers to stop normal service and drain/exit. It is closed
	// only after every in-flight submit has settled.
	stop chan struct{}
	wg   sync.WaitGroup

	// collection is the collection this lane belongs to, recorded at
	// registration so per-sink delivery metrics can be labeled by collection.
	collection string
	// observer, when non-nil, receives each delivery attempt outcome (see
	// SinkDeliveryObserver). It is nil in tests and when metrics are disabled.
	observer SinkDeliveryObserver
	// backpressureObs, when non-nil, receives the current bounded-queue depth at
	// each queue transition plus the backpressure signals (capacity, enqueue
	// wait, queue full) of this lane (see SinkBackpressureObserver). It is nil
	// in tests and when metrics are disabled.
	backpressureObs SinkBackpressureObserver
	// logger, when non-nil, logs each delivery attempt outcome at the delivery
	// boundary (see deliver). It is nil when delivery logging is not wired, in
	// which case the logs are silently skipped.
	logger *slog.Logger

	// submitWG tracks in-flight submit calls so close can wait for every
	// blocked/racing submit to settle before the workers drain and exit.
	submitWG sync.WaitGroup

	// stateMu serializes the closed check with the enqueue and with close
	// itself. It is held only to read/write closed and to register a submit in
	// submitWG; it is released before the (potentially blocking, when the queue
	// is full) enqueue so close never blocks on a backpressured submit.
	stateMu   sync.Mutex
	closed    bool
	closeOnce sync.Once
}

// newLaneFor creates a lane carrying a collection label, an optional delivery
// observer, an optional backpressure observer, and an optional delivery logger,
// used by Register when the dispatcher has them.
func newLaneFor(
	sink *RuntimeSink,
	queueSize,
	workerCount int,
	collection string,
	observer SinkDeliveryObserver,
	backpressureObs SinkBackpressureObserver,
	logger *slog.Logger,
) *lane {
	l := &lane{
		sink:            sink,
		jobs:            make(chan job, queueSize),
		reject:          make(chan struct{}),
		stop:            make(chan struct{}),
		collection:      collection,
		observer:        observer,
		backpressureObs: backpressureObs,
		logger:          logger,
	}
	// Set the configured capacity once, before the workers start, so the lane
	// reports its bounded-queue capacity exactly once at registration. Runs
	// while Register holds the dispatcher mutex: the observer never calls back
	// into the dispatcher.
	if l.backpressureObs != nil {
		l.backpressureObs.SetSinkQueueCapacity(l.collection, l.sink.Type, l.sink.ID, queueSize)
	}
	l.wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go l.run()
	}
	return l
}

// setQueueDepth publishes the current queue depth (len of the jobs channel) to
// the optional backpressure observer. len is safe to read at any time because
// the jobs channel is never closed. depth can never be negative (channel
// length), so no clamping is required.
func (l *lane) setQueueDepth() {
	if l.backpressureObs != nil {
		l.backpressureObs.ObserveSinkQueueDepth(l.collection, l.sink.Type, l.sink.ID, len(l.jobs))
	}
}

// run is a worker goroutine. It services queued jobs and, once the lane is
// closing, drains whatever is still queued before exiting so no accepted job
// is dropped.
func (l *lane) run() {
	defer l.wg.Done()
	for {
		select {
		case <-l.stop:
			// Draining: execute whatever is still queued, then exit.
			for {
				select {
				case j := <-l.jobs:
					l.deliver(j)
					l.setQueueDepth()
				default:
					return
				}
			}
		case j := <-l.jobs:
			l.deliver(j)
			l.setQueueDepth()
		}
	}
}

// deliver runs the sink's Send for a single job and reports the outcome. The
// shared done channel is buffered to the Dispatch's lane count, so this never
// blocks regardless of dispatch goroutine scheduling.
//
// When the lane carries an observer, the delivery is timed around the Send call
// and the outcome is observed. The outcome reflects the per-sink lane result:
// success means the event was accepted by the sink's routing/delivery path
// (which includes a filtered no-op returning nil), failure means Send returned
// an error. Lane-submit failures (context cancellation / lane closed) are never
// observed here because they happen before a transport attempt and settle in the
// dispatcher's submit path instead.
func (l *lane) deliver(j job) {
	start := time.Now()
	// Log the dispatch and per-sink outcome at the delivery boundary, once per
	// delivery attempt. Filtered no-ops (Send returned nil without reaching the
	// transport) log as success, matching the lane's existing success semantics.
	//
	// Levels: dispatch and delivered are DEBUG (per-event detail). A failure is
	// WARN (recoverable — the dispatcher returns the error and the watcher/retry
	// pipeline queues it), and carries the per-sink identity (sinkType/sinkID)
	// the higher layers lack. The failure line includes the transport error
	// verbatim (same text the retry queue and DLQ record in LastError): it may
	// embed the destination endpoint but never credentials, which live in
	// request headers.
	if l.logger != nil {
		l.logger.Debug(
			"Dispatching event",
			"collection", l.collection,
			"eventType", j.record.RecordType,
			"sinkType", l.sink.Type,
			"sinkID", sinkIDOrDash(l.sink.ID),
			"eventID", j.record.EventID,
		)
	}
	err := l.sink.Send(j.ctx, j.record)
	duration := time.Since(start)
	if l.observer != nil {
		l.observer.ObserveSinkDelivery(l.collection, l.sink.Type, duration, err)
	}
	if l.logger != nil {
		if err != nil {
			l.logger.Warn(
				"Sink delivery failed",
				"collection", l.collection,
				"eventType", j.record.RecordType,
				"sinkType", l.sink.Type,
				"sinkID", sinkIDOrDash(l.sink.ID),
				"eventID", j.record.EventID,
				"duration", slog.Duration("duration", duration),
				"error", err,
			)
		} else {
			l.logger.Debug(
				"Event delivered",
				"collection", l.collection,
				"eventType", j.record.RecordType,
				"sinkType", l.sink.Type,
				"sinkID", sinkIDOrDash(l.sink.ID),
				"eventID", j.record.EventID,
				"duration", slog.Duration("duration", duration),
			)
		}
	}
	j.done <- err
}

// sinkIDOrDash renders the sink ID for logs, substituting "-" when the sink
// has no ID (e.g. tests constructing ID-less sinks) so the key=value line stays
// readable instead of rendering an empty sink_id=.
func sinkIDOrDash(id string) string {
	if id == "" {
		return "-"
	}
	return id
}

// submit enqueues a job onto the lane's bounded queue, applying bounded
// backpressure: when the queue is full it blocks until a worker frees
// capacity, until ctx is cancelled, or until the lane is closed. It returns
// nil only when the job was durably handed to the lane (it will be processed);
// any error means the job was not accepted and should be treated as a
// per-sink delivery failure.
//
// Backpressure observability: submit first probes the queue with a single
// non-blocking send (one extra non-blocking select; no behavior change). If it
// wins, the job is placed immediately and the (near-zero) enqueue wait is
// observed. If the queue is full at probe time, the queue-full counter is
// incremented once and submit blocks exactly as before in the slow-path select,
// observing the enqueue wait only on successful placement. Wait duration is
// never observed when the submit fails (ctx cancelled / lane closed).
//
// stateMu is released before the potentially blocking enqueue so a full queue
// never holds close hostage: close flips closed and closes reject, which
// unblocks this select with errLaneClosed. The in-flight registration in
// submitWG lets close wait for this submit to settle before the workers drain
// and exit, so a job accepted while the lane is open is never orphaned.
//
// The select watches reject (not stop): if a submit wins the send race after
// close begins, that is acceptable because workers are not told to exit until
// after submitWG.Wait, so the accepted job is still drained and produces a
// result.
func (l *lane) submit(ctx context.Context, j job) error {
	l.stateMu.Lock()
	if l.closed {
		l.stateMu.Unlock()
		return errLaneClosed
	}
	l.submitWG.Add(1)
	l.stateMu.Unlock()

	defer l.submitWG.Done()

	start := time.Now()

	// Probe once, non-blocking, to detect an already-full queue. A full queue
	// must keep blocking exactly as before; this only records the backpressure
	// condition once per enqueue attempt (it does not spin or re-check while
	// blocked). The non-blocking select is race-free: it either wins a send or
	// observes a full queue at this instant.
	select {
	case l.jobs <- j:
		l.setQueueDepth()
		if l.backpressureObs != nil {
			l.backpressureObs.ObserveSinkEnqueueWait(l.collection, l.sink.Type, l.sink.ID, time.Since(start))
		}
		return nil
	default:
		if l.backpressureObs != nil {
			l.backpressureObs.IncSinkQueueFull(l.collection, l.sink.Type, l.sink.ID)
		}
	}

	// Slow path: the queue was full at probe time. Block exactly as before,
	// observing the enqueue wait only when the job is successfully placed.
	select {
	case l.jobs <- j:
		l.setQueueDepth()
		if l.backpressureObs != nil {
			l.backpressureObs.ObserveSinkEnqueueWait(l.collection, l.sink.Type, l.sink.ID, time.Since(start))
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-l.reject:
		// A close raced with submission; reject rather than enqueue onto a
		// draining lane.
		return errLaneClosed
	}
}

// close stops the worker pool, waits for any accepted (queued or in-flight)
// jobs to complete, and then closes the underlying transport. It is safe to
// call concurrently with Dispatch and is idempotent: only the first call
// drains and closes, later calls are no-ops returning nil.
//
// It never holds stateMu across a blocking operation. The two-phase protocol:
// it flips closed, closes reject (unblocking any backpressured submit with
// errLaneClosed), waits for every in-flight submit to settle via submitWG, and
// only then closes stop so the workers drain the queue and exit. This is what
// prevents the deadlock where a submit blocked on a full queue and a close
// blocked on stateMu would wait on each other forever, and it guarantees a job
// accepted while the lane is open is never orphaned: workers are not signalled
// to exit until every racing submit has either enqueued or been rejected.
func (l *lane) close() error {
	var err error
	l.closeOnce.Do(func() {
		l.stateMu.Lock()
		l.closed = true
		l.stateMu.Unlock()
		// Phase 1: reject/unblock new or backpressured submits.
		close(l.reject)
		// Wait for every in-flight submit to settle (enqueued or rejected) so
		// the drain below sees a quiescent queue and no job is orphaned.
		l.submitWG.Wait()
		// Phase 2: only now tell the workers to stop normal service and drain.
		close(l.stop)
		l.wg.Wait()
		// Queue fully drained and workers exited; publish a final depth of 0 so
		// a removed lane does not leave a stale non-zero series behind.
		l.setQueueDepth()
		err = l.sink.Close()
	})
	return err
}
