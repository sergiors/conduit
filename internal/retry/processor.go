package retry

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/recover"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/streams"
)

// Store is the queue-storage facade the Processor needs. The concrete
// *redis.Client satisfies it implicitly; tests inject a fake to exercise the
// failure paths.
type Store interface {
	DequeueRetry(ctx context.Context, collectionName string, limit int64) ([]redis.RetryEvent, error)
	EnqueueRetry(ctx context.Context, event redis.RetryEvent) error
	RemoveRetryEvent(ctx context.Context, collectionName string, event redis.RetryEvent) error
	SendToDLQ(ctx context.Context, collectionName string, event interface{}) error
	GetRetryQueueLength(ctx context.Context, collectionName string) (int64, error)
	GetDLQLength(ctx context.Context, collectionName string) (int64, error)
}

// Processor handles retry queue processing with exponential backoff
type Processor struct {
	store       Store
	dispatcher  *dispatch.Dispatcher
	interval    time.Duration
	maxRetries  int
	baseDelay   time.Duration
	maxDelay    time.Duration
	collections map[string]bool
	mu          sync.RWMutex

	// Runtime state
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	startMu sync.Mutex
}

// Config holds retry processor configuration
type Config struct {
	Interval   time.Duration // How often to check retry queue
	MaxRetries int           // Maximum retry attempts
	BaseDelay  time.Duration // Base delay for exponential backoff
	MaxDelay   time.Duration // Maximum delay between retries
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Interval:   5 * time.Second,
		MaxRetries: 5,
		BaseDelay:  1 * time.Second,
		MaxDelay:   5 * time.Minute,
	}
}

// NewProcessor creates a new retry processor
func NewProcessor(
	store Store,
	dispatcher *dispatch.Dispatcher,
	cfg Config,
) *Processor {
	return &Processor{
		store:       store,
		dispatcher:  dispatcher,
		interval:    cfg.Interval,
		maxRetries:  cfg.MaxRetries,
		baseDelay:   cfg.BaseDelay,
		maxDelay:    cfg.MaxDelay,
		collections: make(map[string]bool),
	}
}

// Start begins processing the retry queue. It is idempotent: calling Start
// more than once is a no-op and returns nil.
func (p *Processor) Start(ctx context.Context) error {
	p.startMu.Lock()
	defer p.startMu.Unlock()

	if p.started {
		return nil
	}

	log.Println("Retry processor starting...")

	p.ctx, p.cancel = context.WithCancel(ctx)
	p.started = true

	p.wg.Add(1)
	go func() {
		// Backstop: a panic that slips past per-tick isolation would otherwise
		// crash the process. Protect catches it on this goroutine so the
		// deferred wg.Done still runs and Stop's Wait cannot hang.
		defer p.wg.Done()
		recover.Protect("retry:processor", func() {
			p.processLoop(p.ctx)
		})
	}()

	return nil
}

// Stop stops the retry processor gracefully. It cancels the processor's own
// context and waits for processLoop to exit, so an in-flight processQueue pass
// completes before returning. Stop is idempotent: calling it more than once
// (or before Start) is a no-op and returns nil.
func (p *Processor) Stop(ctx context.Context) error {
	p.startMu.Lock()
	if !p.started {
		p.startMu.Unlock()
		return nil
	}
	p.started = false
	p.cancel()
	p.startMu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// processLoop continuously processes retry queue
func (p *Processor) processLoop(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Retry processor stopping...")
			return
		case <-ticker.C:
			// A panic while processing the queue must not kill the loop; the
			// next tick retries.
			if _, panicked := recover.ProtectErr("retry:processor", func() error {
				p.processQueue(ctx)
				return nil
			}); panicked {
				log.Println("Retry queue processing panicked; continuing loop")
			}
		}
	}
}

// processQueue processes all collections' retry queues
func (p *Processor) processQueue(ctx context.Context) {
	// Get all collections with retry queues
	collections := p.getKnownCollections(ctx)
	for _, collection := range collections {
		p.processCollectionQueue(ctx, collection)
	}
}

// getKnownCollections returns collections that have retry queues
func (p *Processor) getKnownCollections(ctx context.Context) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	collections := make([]string, 0, len(p.collections))
	for collection := range p.collections {
		collections = append(collections, collection)
	}
	return collections
}

// RegisterCollection adds a collection to the known collections list
func (p *Processor) RegisterCollection(collectionName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.collections[collectionName] = true
}

// UnregisterCollection removes a collection from the known collections list
func (p *Processor) UnregisterCollection(collectionName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.collections, collectionName)
}

// ProcessCollectionQueue processes retry queue for a specific collection
func (p *Processor) ProcessCollectionQueue(ctx context.Context, collectionName string) {
	p.processCollectionQueue(ctx, collectionName)
}

func (p *Processor) processCollectionQueue(ctx context.Context, collectionName string) {
	// Get events ready for retry
	events, err := p.store.DequeueRetry(ctx, collectionName, 10)
	if err != nil {
		log.Printf("Failed to dequeue retry events for %s: %v", collectionName, err)
		return
	}

	for _, event := range events {
		p.processRetryEvent(ctx, collectionName, event)
	}
}

// processRetryEvent processes a single retry event
func (p *Processor) processRetryEvent(ctx context.Context, collectionName string, event redis.RetryEvent) {
	// Bookkeeping writes (Remove + Enqueue) are paired operations: completing
	// only half of a pair loses the event. Use a detached context so they
	// survive a shutdown that cancels the live ctx mid-event.
	bkctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	// Check if max retries exceeded
	if event.RetryCount >= event.MaxRetries {
		log.Printf("Event exceeded max retries (%d), sending to DLQ: %s", event.MaxRetries, collectionName)
		if p.store == nil {
			log.Printf("DLQ not available (nil store), event retained in queue: %s", collectionName)
			return
		}
		// Enqueue-first style ordering: only remove the event after a
		// successful DLQ push. If SendToDLQ fails, the event stays queued and
		// is retried next tick; the max-retries branch will attempt the DLQ
		// push again. This is safe because the DLQ push is at-least-once
		// (a stale duplicate in the DLQ is acceptable; losing the event is not).
		if err := p.store.SendToDLQ(bkctx, collectionName, event.EventData); err != nil {
			log.Printf("Failed to send to DLQ: %v", err)
			return
		}
		// Remove from retry queue only after a successful DLQ push. If the
		// removal itself fails, a stale duplicate remains in the retry queue;
		// that is intentional under at-least-once semantics, since the event
		// is already durably present in the DLQ and a duplicate is preferable
		// to a loss.
		if err := p.store.RemoveRetryEvent(bkctx, collectionName, event); err != nil {
			log.Printf("Failed to remove event from retry queue: %v", err)
		}
		return
	}

	// Try to dispatch again - parse event data from JSON
	record, err := streams.ParseStreamRecord(event.EventData)
	if err != nil {
		log.Printf("Failed to parse stream record from retry queue: %v", err)
		// Remove invalid event from queue
		if p.store != nil {
			if err := p.store.RemoveRetryEvent(bkctx, collectionName, event); err != nil {
				log.Printf("Failed to remove invalid event from retry queue: %v", err)
			}
		}
		return
	}

	if err := p.dispatcher.Dispatch(ctx, collectionName, *record); err != nil {
		log.Printf("Retry %d/%d failed for %s: %v", event.RetryCount+1, event.MaxRetries, collectionName, err)

		if p.store == nil {
			return
		}

		// Re-queue with exponential backoff. Enqueue the UPDATED event first
		// (RetryCount+1, new NextRetryAt); only after a successful enqueue do we
		// remove the old member. If enqueueing the updated event fails, the old
		// member is left in place and retried next tick -- doing it the other way
		// around (remove first) would lose the event if the enqueue failed. If
		// the enqueue succeeds but the remove fails, a stale duplicate remains;
		// a duplicate is acceptable under at-least-once, but a loss is not.
		original := event
		event.RetryCount++
		event.NextRetryAt = p.calculateNextRetry(event.RetryCount)
		if err := p.store.EnqueueRetry(bkctx, event); err != nil {
			log.Printf("Failed to re-queue retry event: %v", err)
			return
		}
		if err := p.store.RemoveRetryEvent(bkctx, collectionName, original); err != nil {
			log.Printf("Failed to remove old retry event: %v", err)
		}
		return
	}

	// Success - event processed, remove from retry queue
	log.Printf("Retry succeeded for %s after %d attempts", collectionName, event.RetryCount+1)
	if p.store != nil {
		if err := p.store.RemoveRetryEvent(bkctx, collectionName, event); err != nil {
			log.Printf("Failed to remove event from retry queue: %v", err)
		}
	}
}

// calculateNextRetry calculates next retry time with exponential backoff
func (p *Processor) calculateNextRetry(retryCount int) time.Time {
	// Exponential backoff: baseDelay * 2^(retryCount-1)
	delay := float64(p.baseDelay) * math.Pow(2, float64(retryCount-1))

	// Cap at maxDelay
	if delay > float64(p.maxDelay) {
		delay = float64(p.maxDelay)
	}

	return time.Now().Add(time.Duration(delay))
}

// GetRetryQueueLength returns the total retry queue length for a collection
func (p *Processor) GetRetryQueueLength(ctx context.Context, collectionName string) (int64, error) {
	if p.store == nil {
		return 0, nil
	}
	return p.store.GetRetryQueueLength(ctx, collectionName)
}

// GetDLQLength returns the DLQ length for a collection
func (p *Processor) GetDLQLength(ctx context.Context, collectionName string) (int64, error) {
	if p.store == nil {
		return 0, nil
	}
	return p.store.GetDLQLength(ctx, collectionName)
}
