package retry

import (
	"context"
	"log"
	"math"
	"sync"
	"time"

	"github.com/sergiors/relay/internal/dispatch"
	"github.com/sergiors/relay/internal/redis"
	"github.com/sergiors/relay/internal/streams"
)

// Processor handles retry queue processing with exponential backoff
type Processor struct {
	redisClient *redis.Client
	dispatcher  *dispatch.Dispatcher
	interval    time.Duration
	maxRetries  int
	baseDelay   time.Duration
	maxDelay    time.Duration
	tables      map[string]bool
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
		tables:      make(map[string]bool),
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

// processQueue processes all tables' retry queues
func (p *Processor) processQueue(ctx context.Context) {
	// Get all tables with retry queues
	tables := p.getKnownTables(ctx)
	for _, table := range tables {
		p.processTableQueue(ctx, table)
	}
}

// getKnownTables returns tables that have retry queues
func (p *Processor) getKnownTables(ctx context.Context) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tables := make([]string, 0, len(p.tables))
	for table := range p.tables {
		tables = append(tables, table)
	}
	return tables
}

// RegisterTable adds a table to the known tables list
func (p *Processor) RegisterTable(tableName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tables[tableName] = true
}

// UnregisterTable removes a table from the known tables list
func (p *Processor) UnregisterTable(tableName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.tables, tableName)
}

// ProcessTableQueue processes retry queue for a specific table
func (p *Processor) ProcessTableQueue(ctx context.Context, tableName string) {
	p.processTableQueue(ctx, tableName)
}

func (p *Processor) processTableQueue(ctx context.Context, tableName string) {
	// Get event IDs ready for retry
	eventIDs, err := p.redisClient.DequeueRetry(ctx, tableName, 10)
	if err != nil {
		log.Printf("Failed to dequeue retry events for %s: %v", tableName, err)
		return
	}

	for _, eventID := range eventIDs {
		p.processRetryEvent(ctx, tableName, eventID)
	}
}

// processRetryEvent processes a single retry event by ID
func (p *Processor) processRetryEvent(ctx context.Context, tableName string, eventID string) {
	// Get full event data
	event, err := p.redisClient.GetRetryEvent(ctx, tableName, eventID)
	if err != nil {
		log.Printf("Failed to get retry event data for %s: %v", eventID, err)
		return
	}

	// Check if max retries exceeded
	if event.RetryCount >= event.MaxRetries {
		log.Printf("Event exceeded max retries (%d), sending to DLQ: %s", event.MaxRetries, tableName)
		if p.redisClient != nil {
			if err := p.redisClient.SendToDLQ(ctx, tableName, event.EventData); err != nil {
				log.Printf("Failed to send to DLQ: %v", err)
			}
			// Remove from retry queue after sending to DLQ
			if err := p.redisClient.RemoveRetryEvent(ctx, tableName, eventID); err != nil {
				log.Printf("Failed to remove event from retry queue: %v", err)
			}
			// Remove event data
			if err := p.redisClient.RemoveRetryEventData(ctx, tableName, eventID); err != nil {
				log.Printf("Failed to remove retry event data: %v", err)
			}
		} else {
			log.Printf("DLQ not available (nil redis client), event lost: %s", tableName)
		}
		return
	}

	// Try to dispatch again - parse event data from JSON
	record, err := streams.ParseStreamRecord(event.EventData)
	if err != nil {
		log.Printf("Failed to parse stream record from retry queue: %v", err)
		return
	}

	if err := p.dispatcher.Dispatch(ctx, tableName, *record); err != nil {
		log.Printf("Retry %d/%d failed for %s: %v", event.RetryCount+1, event.MaxRetries, tableName, err)

		// Re-queue with exponential backoff - first remove old entry, then add new one
		if p.redisClient != nil {
			if err := p.redisClient.RemoveRetryEvent(ctx, tableName, eventID); err != nil {
				log.Printf("Failed to remove old retry event: %v", err)
			}
			event.RetryCount++
			event.NextRetryAt = p.calculateNextRetry(event.RetryCount)
			// Update event data
			if err := p.redisClient.StoreRetryEventData(ctx, tableName, eventID, *event); err != nil {
				log.Printf("Failed to update retry event data: %v", err)
			}
			if err := p.redisClient.EnqueueRetry(ctx, *event); err != nil {
				log.Printf("Failed to re-queue retry event: %v", err)
			}
		}
		return
	}

	// Success - event processed, remove from retry queue
	log.Printf("Retry succeeded for %s after %d attempts", tableName, event.RetryCount+1)
	if p.redisClient != nil {
		if err := p.redisClient.RemoveRetryEvent(ctx, tableName, eventID); err != nil {
			log.Printf("Failed to remove event from retry queue: %v", err)
		}
		// Remove event data
		if err := p.redisClient.RemoveRetryEventData(ctx, tableName, eventID); err != nil {
			log.Printf("Failed to remove retry event data: %v", err)
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

// GetRetryQueueLength returns the total retry queue length for a table
func (p *Processor) GetRetryQueueLength(ctx context.Context, tableName string) (int64, error) {
	if p.redisClient == nil {
		return 0, nil
	}
	return p.redisClient.GetRetryQueueLength(ctx, tableName)
}

// GetDLQLength returns the DLQ length for a table
func (p *Processor) GetDLQLength(ctx context.Context, tableName string) (int64, error) {
	if p.redisClient == nil {
		return 0, nil
	}
	return p.redisClient.GetDLQLength(ctx, tableName)
}
