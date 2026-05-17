package retry

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/sergiors/conduit/internal/dispatch"
	"github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/streams"
)

// Processor handles retry queue processing with exponential backoff
type Processor struct {
	redisClient *redis.Client
	dispatcher  *dispatch.Dispatcher
	interval    time.Duration
	maxRetries  int
	baseDelay   time.Duration
	maxDelay    time.Duration
	collections map[string]bool
	mu          sync.RWMutex
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
	redisClient *redis.Client,
	dispatcher *dispatch.Dispatcher,
	cfg Config,
) *Processor {
	return &Processor{
		redisClient: redisClient,
		dispatcher:  dispatcher,
		interval:    cfg.Interval,
		maxRetries:  cfg.MaxRetries,
		baseDelay:   cfg.BaseDelay,
		maxDelay:    cfg.MaxDelay,
		collections: make(map[string]bool),
	}
}

// Start begins processing the retry queue
func (p *Processor) Start(ctx context.Context) error {
	log.Println("Retry processor starting...")

	go p.processLoop(ctx)

	return nil
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
			p.processQueue(ctx)
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
	events, err := p.redisClient.DequeueRetry(ctx, collectionName, 10)
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
	// Check if max retries exceeded
	if event.RetryCount >= event.MaxRetries {
		log.Printf("Event exceeded max retries (%d), sending to DLQ: %s", event.MaxRetries, collectionName)
		if p.redisClient != nil {
			if err := p.redisClient.SendToDLQ(ctx, collectionName, event.EventData); err != nil {
				log.Printf("Failed to send to DLQ: %v", err)
			}
			// Remove from retry queue after sending to DLQ
			if err := p.redisClient.RemoveRetryEvent(ctx, collectionName, event); err != nil {
				log.Printf("Failed to remove event from retry queue: %v", err)
			}
		} else {
			log.Printf("DLQ not available (nil redis client), event lost: %s", collectionName)
		}
		return
	}

	// Try to dispatch again - parse event data from JSON
	record, err := streams.ParseStreamRecord(event.EventData)
	if err != nil {
		log.Printf("Failed to parse stream record from retry queue: %v", err)
		// Remove invalid event from queue
		if p.redisClient != nil {
			if err := p.redisClient.RemoveRetryEvent(ctx, collectionName, event); err != nil {
				log.Printf("Failed to remove invalid event from retry queue: %v", err)
			}
		}
		return
	}

	if err := p.dispatcher.Dispatch(ctx, collectionName, *record); err != nil {
		log.Printf("Retry %d/%d failed for %s: %v", event.RetryCount+1, event.MaxRetries, collectionName, err)

		// Re-queue with exponential backoff - remove old, add new
		if p.redisClient != nil {
			if err := p.redisClient.RemoveRetryEvent(ctx, collectionName, event); err != nil {
				log.Printf("Failed to remove old retry event: %v", err)
			}
			event.RetryCount++
			event.NextRetryAt = p.calculateNextRetry(event.RetryCount)
			if err := p.redisClient.EnqueueRetry(ctx, event); err != nil {
				log.Printf("Failed to re-queue retry event: %v", err)
			}
		}
		return
	}

	// Success - event processed, remove from retry queue
	log.Printf("Retry succeeded for %s after %d attempts", collectionName, event.RetryCount+1)
	if p.redisClient != nil {
		if err := p.redisClient.RemoveRetryEvent(ctx, collectionName, event); err != nil {
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
	if p.redisClient == nil {
		return 0, nil
	}
	return p.redisClient.GetRetryQueueLength(ctx, collectionName)
}

// GetDLQLength returns the DLQ length for a collection
func (p *Processor) GetDLQLength(ctx context.Context, collectionName string) (int64, error) {
	if p.redisClient == nil {
		return 0, nil
	}
	return p.redisClient.GetDLQLength(ctx, collectionName)
}
