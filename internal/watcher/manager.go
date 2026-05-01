package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sergiors/relay/internal/dispatch"
	redisclient "github.com/sergiors/relay/internal/redis"
	"github.com/sergiors/relay/internal/retry"
	"github.com/sergiors/relay/internal/streams"
	"github.com/sergiors/relay/internal/tables"
	"go.mongodb.org/mongo-driver/mongo"
)

// Manager manages CDC watchers for all enabled tables
type Manager struct {
	mongoClient    *mongo.Client
	database       string
	tableStore     *tables.Store
	redisClient    *redisclient.Client
	dispatcher     Dispatcher
	retryProcessor *retry.Processor
	watchers       map[string]*Watcher
	mu             sync.RWMutex
	syncInterval   time.Duration
	pubsub         *redis.PubSub
	configChan     <-chan *redis.Message
}

// Dispatcher interface for decoupling
type Dispatcher interface {
	Dispatch(ctx context.Context, tableName string, record streams.StreamRecord) error
}

// Config holds watcher manager configuration
type Config struct {
	SyncInterval time.Duration // How often to sync with system.tables
	HTTPEndpoint string        // HTTP endpoint for creating destinations
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		SyncInterval: 30 * time.Second,
	}
}

// NewManager creates a new watcher manager
func NewManager(
	mongoClient *mongo.Client,
	database string,
	tableStore *tables.Store,
	redisClient *redisclient.Client,
	dispatcher Dispatcher,
	retryProcessor *retry.Processor,
	cfg Config,
) *Manager {
	return &Manager{
		mongoClient:  mongoClient,
		database:     database,
		tableStore:   tableStore,
		redisClient:  redisClient,
		dispatcher:   dispatcher,
		retryProcessor: retryProcessor,
		watchers:     make(map[string]*Watcher),
		syncInterval: cfg.SyncInterval,
	}
}

// Start initializes and starts all watchers
func (m *Manager) Start(ctx context.Context) error {
	log.Println("Watcher manager starting...")

	// Initial load of stream-enabled tables
	tables, err := m.tableStore.ListStreamEnabled(ctx)
	if err != nil {
		return fmt.Errorf("load tables: %w", err)
	}

	log.Printf("Found %d stream-enabled tables", len(tables))

	// Start watcher for each enabled table
	for _, table := range tables {
		if err := m.startWatcher(ctx, table); err != nil {
			log.Printf("Failed to start watcher for %s: %v", table.TableName, err)
		}
	}

	// Subscribe to config change notifications
	pubsub, err := m.redisClient.SubscribeConfigChanges(ctx)
	if err != nil {
		log.Printf("Failed to subscribe to config changes: %v", err)
	} else {
		m.pubsub = pubsub
		m.configChan = pubsub.Channel()
		go m.configChangeLoop(ctx)
	}

	// Start sync loop
	go m.syncLoop(ctx)

	return nil
}

// Stop stops all watchers
func (m *Manager) Stop(ctx context.Context) error {
	log.Println("Watcher manager stopping...")

	m.mu.Lock()
	defer m.mu.Unlock()

	var lastErr error
	for tableName, watcher := range m.watchers {
		if err := watcher.Stop(ctx); err != nil {
			log.Printf("Failed to stop watcher for %s: %v", tableName, err)
			lastErr = err
		}
		delete(m.watchers, tableName)
	}

	// Close Pub/Sub subscription
	if m.pubsub != nil {
		if err := m.pubsub.Close(); err != nil {
			log.Printf("Failed to close Pub/Sub: %v", err)
			lastErr = err
		}
	}

	return lastErr
}

// startWatcher creates and starts a watcher for a table
func (m *Manager) startWatcher(ctx context.Context, table tables.Table) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if _, exists := m.watchers[table.TableName]; exists {
		log.Printf("Watcher for %s already running", table.TableName)
		return nil
	}

	log.Printf("Starting watcher for table: %s", table.TableName)

	// Register destinations for this table
	if err := m.registerDestinations(ctx, table.TableName, table.Destinations); err != nil {
		log.Printf("Failed to register destinations for %s: %v", table.TableName, err)
	}

	// Get resume token from Redis
	resumeToken, err := m.redisClient.GetResumeToken(ctx, table.TableName)
	if err != nil {
		log.Printf("Failed to get resume token for %s: %v", table.TableName, err)
	}

	// Create watcher
	watcher := NewWatcher(
		m.mongoClient,
		m.database,
		table.TableName,
		table.OldImage,
		resumeToken,
		m.redisClient,
	)

	// Start watcher with event handler
	if err := watcher.Start(ctx, func(record streams.StreamRecord) error {
		return m.handleEvent(ctx, table.TableName, record)
	}); err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}

	m.watchers[table.TableName] = watcher

	// Register table with retry processor
	if m.retryProcessor != nil {
		m.retryProcessor.RegisterTable(table.TableName)
	}

	return nil
}

// stopWatcher stops and removes a watcher
func (m *Manager) stopWatcher(ctx context.Context, tableName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	watcher, exists := m.watchers[tableName]
	if !exists {
		return nil
	}

	log.Printf("Stopping watcher for table: %s", tableName)

	if err := watcher.Stop(ctx); err != nil {
		return err
	}

	delete(m.watchers, tableName)

	// Unregister table from retry processor
	if m.retryProcessor != nil {
		m.retryProcessor.UnregisterTable(tableName)
	}

	return nil
}

// handleEvent processes an event with idempotency and retry logic
func (m *Manager) handleEvent(ctx context.Context, tableName string, record streams.StreamRecord) error {
	// Generate event ID: {table}-{type}-{timestamp}
	eventID := fmt.Sprintf("%s-%s-%d", tableName, record.RecordType, record.Timestamp.UnixNano())

	// Check idempotency
	processed, err := m.redisClient.IsProcessed(ctx, eventID)
	if err != nil {
		log.Printf("Failed to check idempotency: %v", err)
		// Continue processing - better duplicate than lost
	}
	if processed {
		log.Printf("Event %s already processed (idempotent skip)", eventID)
		return nil
	}

	// Dispatch to destinations
	if err := m.dispatcher.Dispatch(ctx, tableName, record); err != nil {
		log.Printf("Dispatch failed for %s: %v", eventID, err)
		// Queue for retry
		return m.queueRetry(ctx, tableName, record, eventID)
	}

	// Mark as processed
	// Use 24h TTL for idempotency key
	if err := m.redisClient.MarkProcessed(ctx, eventID, 24*time.Hour); err != nil {
		log.Printf("Failed to mark event as processed: %v", err)
	}

	return nil
}

// queueRetry adds failed event to retry queue with exponential backoff
func (m *Manager) queueRetry(ctx context.Context, tableName string, record streams.StreamRecord, eventID string) error {
	retryID := fmt.Sprintf("%s:%s", tableName, eventID)

	// Marshal the stream record to JSON
	eventData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal stream record: %w", err)
	}

	retryEvent := redisclient.RetryEvent{
		ID:          retryID,
		TableName:   tableName,
		EventData:   eventData,
		RetryCount:  0,
		MaxRetries:  5,
		NextRetryAt: time.Now().Add(time.Second), // First retry after 1s
	}

	return m.redisClient.EnqueueRetry(ctx, retryEvent)
}

// configChangeLoop listens for config change notifications and triggers immediate sync
func (m *Manager) configChangeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-m.configChan:
			if !ok {
				return
			}
			tableName := msg.Payload
			log.Printf("Config change detected for table: %s", tableName)
			m.syncWithTables(ctx)
		}
	}
}

// syncLoop periodically syncs watchers with config.tables
func (m *Manager) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(m.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.syncWithTables(ctx)
		}
	}
}

// syncWithTables diffs current watchers with config.tables
func (m *Manager) syncWithTables(ctx context.Context) {
	log.Println("Syncing watchers with config.tables...")

	// Fetch current stream-enabled tables
	tableList, err := m.tableStore.ListStreamEnabled(ctx)
	if err != nil {
		log.Printf("Failed to list tables: %v", err)
		return
	}

	// Build set of enabled table names
	enabledSet := make(map[string]tables.Table)
	for _, table := range tableList {
		enabledSet[table.TableName] = table
	}

	// Get current watchers
	m.mu.RLock()
	currentWatchers := make(map[string]bool)
	for tableName := range m.watchers {
		currentWatchers[tableName] = true
	}
	m.mu.RUnlock()

	// Start watchers for new tables and register destinations
	for tableName, table := range enabledSet {
		if !currentWatchers[tableName] {
			// Register destinations for this table
			if err := m.registerDestinations(ctx, tableName, table.Destinations); err != nil {
				log.Printf("Failed to register destinations for %s: %v", tableName, err)
			}

			if err := m.startWatcher(ctx, table); err != nil {
				log.Printf("Failed to start watcher for %s: %v", tableName, err)
			}
		}
	}

	// Stop watchers for disabled tables
	for tableName := range currentWatchers {
		if _, exists := enabledSet[tableName]; !exists {
			if err := m.stopWatcher(ctx, tableName); err != nil {
				log.Printf("Failed to stop watcher for %s: %v", tableName, err)
			}
		}
	}

	log.Printf("Sync complete: %d active watchers", len(enabledSet))
}

// registerDestinations registers event destinations for a table
func (m *Manager) registerDestinations(ctx context.Context, tableName string, destinations []tables.DestinationConfig) error {
	for _, dest := range destinations {
		switch dest.Type {
		case "http":
			if dest.Endpoint == "" {
				log.Printf("HTTP destination requested but endpoint not set for table %s", tableName)
				continue
			}
			// Default to all event types if not specified
			eventTypes := dest.EventTypes
			if len(eventTypes) == 0 {
				eventTypes = []string{"INSERT", "MODIFY", "REMOVE"}
			}
			httpDest, err := dispatch.NewHTTPDestination(dest.Endpoint, dest.BearerToken, eventTypes)
			if err != nil {
				log.Printf("Failed to create HTTP destination for %s: %v", tableName, err)
				continue
			}
			// Cast to Dispatcher interface to access Register method
			if d, ok := m.dispatcher.(*dispatch.Dispatcher); ok {
				d.Register(tableName, httpDest)
				log.Printf("Registered HTTP destination for table %s -> %s", tableName, dest.Endpoint)
			}
		case "eventbridge":
			// TODO: Add EventBridge destination when configured
			log.Printf("EventBridge destination not yet implemented for table %s", tableName)
		default:
			log.Printf("Unknown destination type: %s for table %s", dest.Type, tableName)
		}
	}
	return nil
}

// GetActiveWatchers returns the count of active watchers
func (m *Manager) GetActiveWatchers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.watchers)
}

// GetWatcherStats returns stats for a specific watcher
func (m *Manager) GetWatcherStats(tableName string) (WatcherStats, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	watcher, exists := m.watchers[tableName]
	if !exists {
		return WatcherStats{}, false
	}

	return watcher.GetStats(), true
}

// WatcherStats holds statistics for a watcher
type WatcherStats struct {
	StartTime       time.Time
	EventsProcessed int64
	LastError       error
	LastErrorTime   time.Time
}