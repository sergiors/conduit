package dispatch

import (
	"context"
	"sync"

	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/streams"
)

// DefaultQueueSize and DefaultWorkerCount are the per-sink lane defaults used
// by NewDispatcher (and by NewDispatcherWithConfig when the config omits a
// value). QueueSize bounds the number of events awaiting delivery by a sink's
// worker pool; WorkerCount is the number of delivery workers per sink lane.
const (
	DefaultQueueSize   = 1024
	DefaultWorkerCount = 4
)

// Config is the dispatcher-wide concurrency configuration that each sink lane
// is built from. One dispatcher-wide default keeps the configuration surface
// small; per-sink tuning is a documented future option, not a requirement.
type Config struct {
	// QueueSize is the bounded job-queue capacity of each sink lane (default
	// DefaultQueueSize). A full queue applies backpressure to Dispatch: it
	// blocks (until capacity frees or ctx is cancelled) rather than dropping.
	QueueSize int
	// WorkerCount is the number of delivery workers per sink lane (default
	// DefaultWorkerCount). It bounds how many events each sink delivers
	// concurrently.
	WorkerCount int
}

// Dispatcher routes stream records to the runtime sinks configured for each
// collection. Each RuntimeSink owns a per-sink execution lane (bounded queue +
// worker pool) so delivery to sinks happens concurrently and a slow sink
// cannot block delivery to the others.
type Dispatcher struct {
	sinks map[string][]*lane
	mu    sync.RWMutex

	cfg Config
}

// NewDispatcher creates a new event dispatcher using default per-sink lane
// configuration.
func NewDispatcher() *Dispatcher {
	return NewDispatcherWithConfig(Config{})
}

// NewDispatcherWithConfig creates a new event dispatcher with the given
// per-sink lane configuration. Zero or negative values are sanitized to the
// defaults.
func NewDispatcherWithConfig(cfg Config) *Dispatcher {
	return &Dispatcher{
		sinks: make(map[string][]*lane),
		cfg:   sanitizeConfig(cfg),
	}
}

// sanitizeConfig replaces zero/negative values with the dispatcher defaults so
// a partial or zero-value Config still behaves correctly.
func sanitizeConfig(cfg Config) Config {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = DefaultWorkerCount
	}
	return cfg
}

// laneCount returns the queue size and worker count for a single lane's config.
func (d *Dispatcher) laneCount() (queueSize, workerCount int) {
	return d.cfg.QueueSize, d.cfg.WorkerCount
}

// Register adds a runtime sink for a collection, creating and starting its
// delivery lane.
func (d *Dispatcher) Register(collection string, sink *RuntimeSink) {
	queueSize, workerCount := d.laneCount()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sinks[collection] == nil {
		d.sinks[collection] = make([]*lane, 0)
	}
	d.sinks[collection] = append(d.sinks[collection], newLane(sink, queueSize, workerCount))
}

// Dispatch sends a stream record to all runtime sinks for a collection.
//
// Settlement contract: a nil return means the event was delivered to every
// sink for the collection (sinks that filter the event out also return nil,
// which counts as settled — nothing was supposed to be delivered). Therefore
// every Transport.Send implementation MUST return an error whenever the event
// was not durably accepted by the downstream system (e.g. any non-2xx HTTP
// response); returning nil for a silently dropped event would break the
// pipeline's at-least-once guarantee by making the event appear settled.
// If any sink fails, an error is returned so the caller knows the delivery was
// incomplete.
//
// Concurrency model: the sinks for the collection are snapshotted under a read
// lock, and a job is submitted concurrently to each sink's lane. Submission
// attempts happen in parallel so a full or slow sink lane does not prevent the
// event from being offered to the other lanes. A full lane applies bounded
// backpressure: submission blocks until capacity frees, ctx is cancelled, or
// the lane is closed. Dispatch then waits for every submitted job to complete
// and returns a non-nil error if any matching sink delivery failed or any job
// could not be submitted (context cancellation or a lane closed concurrently).
func (d *Dispatcher) Dispatch(ctx context.Context, collection string, record streams.StreamRecord) error {
	d.mu.RLock()
	lanes := d.sinks[collection]
	d.mu.RUnlock()

	if len(lanes) == 0 {
		return nil
	}

	// Shared, buffered result channel sized to the number of lanes so workers
	// never block emitting outcomes, regardless of scheduling order.
	done := make(chan error, len(lanes))

	// Submit one job per lane concurrently so a full/slow lane cannot prevent
	// submission to the others. Exactly one result per lane is produced: on a
	// successful submit the lane worker emits the Send outcome; on a failed
	// submit (ctx cancelled / lane closed) this goroutine emits the submit error.
	submitWG := sync.WaitGroup{}
	for _, l := range lanes {
		l := l
		submitWG.Add(1)
		go func() {
			defer submitWG.Done()
			if err := l.submit(ctx, job{ctx: ctx, record: record, done: done}); err != nil {
				done <- err
			}
		}()
	}

	// Wait for every submission attempt to resolve (accepted, backpressured, or
	// rejected by ctx/lane close) before collecting worker outcomes; otherwise
	// we could read from done before all lanes have had a chance to enqueue.
	submitWG.Wait()

	var lastErr error
	for range lanes {
		if err := <-done; err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// Close closes all runtime sinks (and their transports), stopping all lanes.
// It is idempotent: calling Close more than once is safe, and it never
// deadlocks with a concurrent Dispatch because each lane drains its accepted
// jobs (including in-flight ones) before closing its transport.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var lastErr error
	for _, lns := range d.sinks {
		for _, l := range lns {
			if err := l.close(); err != nil {
				lastErr = err
			}
		}
	}
	d.sinks = make(map[string][]*lane)
	return lastErr
}

// Update atomically replaces the runtime configuration of an existing sink
// (matched by its stable identity), preserving its transport. The lane keeps
// running: only the RuntimeSink's filter/event-type snapshot is swapped, so
// dispatch is not interrupted and in-flight jobs observe either the old or the
// new snapshot, never a torn one. Returns false when no sink with the given
// identity is registered.
func (d *Dispatcher) Update(collection string, sink collections.Sink) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	lanes, ok := d.sinks[collection]
	if !ok {
		return false
	}
	for _, l := range lanes {
		if l.sink.Key() == sink.Identity() {
			l.sink.UpdateConfig(sink)
			return true
		}
	}
	return false
}

// Remove removes a single runtime sink by its stable key, closing its lane
// (and transport) first. It is safe to call concurrently with Dispatch: the
// lane drains any accepted jobs before closing, so an in-flight event is
// neither dropped nor left hanging.
func (d *Dispatcher) Remove(collection, key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	lanes, ok := d.sinks[collection]
	if !ok {
		return
	}

	for i, l := range lanes {
		if l.sink.Key() == key {
			l.close()
			d.sinks[collection] = append(lanes[:i], lanes[i+1:]...)
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

	if lanes, ok := d.sinks[collection]; ok {
		for _, l := range lanes {
			l.close()
		}
		delete(d.sinks, collection)
	}
}
