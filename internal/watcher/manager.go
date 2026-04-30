package watcher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sergiors/relay/internal/redis"
	"github.com/sergiors/relay/internal/streams"
	"github.com/sergiors/relay/internal/tables"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Manager manages CDC watchers for all enabled tables
type Manager struct {
	mongoClient  *mongo.Client
	database     string
	tableStore   *tables.Store
	redisClient  *redis.Client
	dispatcher   Dispatcher
	watchers     map[string]*Watcher
	mu           sync.RWMutex
	syncInterval time.Duration
	eventHandler func(tableName string, record streams.StreamRecord) error
}

// Dispatcher interface for decoupling
type Dispatcher interface {
	Dispatch(ctx context.Context, tableName string, record streams.StreamRecord) error
}

// Config holds watcher manager configuration
type Config struct {
	SyncInterval time.Duration // How often to sync with system.tables
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
	redisClient *redis.Client,
	dispatcher Dispatcher,
	cfg Config,
) *Manager {
	return &Manager{
		mongoClient:  mongoClient,
		database:     database,
		tableStore:   tableStore,
		redisClient:  redisClient,
		dispatcher:   dispatcher,
		watchers:     make(map[string]*Watcher),
		syncInterval: cfg.SyncInterval,
	}
}

// Start initializes and starts all watchers
func (m *Manager) Start(ctx context.Context) error {
	log.Println("Watcher manager starting...")

	// Wait for replica set to be ready (required for change streams)
	if err := m.waitForReplicaSet(ctx); err != nil {
		return fmt.Errorf("wait for replica set: %w", err)
	}

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
	return nil
}

// handleEvent processes an event with idempotency and retry logic
func (m *Manager) handleEvent(ctx context.Context, tableName string, record streams.StreamRecord) error {
	// Generate event ID (could be from record metadata or generated)
	eventID := fmt.Sprintf("%s-%s-%d", tableName, record.RecordType, record.Timestamp.UnixNano())

	// Check idempotency
	processed, err := m.redisClient.IsProcessed(ctx, tableName, eventID)
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
	if err := m.redisClient.MarkProcessed(ctx, tableName, eventID, 24*time.Hour); err != nil {
		log.Printf("Failed to mark event as processed: %v", err)
	}

	return nil
}

// queueRetry adds failed event to retry queue with exponential backoff
func (m *Manager) queueRetry(ctx context.Context, tableName string, record streams.StreamRecord, eventID string) error {
	retryEvent := redis.RetryEvent{
		TableName:   tableName,
		Event:       record,
		RetryCount:  0,
		MaxRetries:  5,
		NextRetryAt: time.Now().Add(time.Second), // First retry after 1s
	}

	return m.redisClient.EnqueueRetry(ctx, retryEvent)
}

// syncLoop periodically syncs watchers with system.tables
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

// syncWithTables diffs current watchers with system.tables
func (m *Manager) syncWithTables(ctx context.Context) {
	log.Println("Syncing watchers with system.tables...")

	// Fetch current stream-enabled tables
	tables, err := m.tableStore.ListStreamEnabled(ctx)
	if err != nil {
		log.Printf("Failed to list tables: %v", err)
		return
	}

	// Build set of enabled table names
	enabledSet := make(map[string]bool)
	for _, table := range tables {
		enabledSet[table.TableName] = true
	}

	// Get current watchers
	m.mu.RLock()
	currentWatchers := make(map[string]bool)
	for tableName := range m.watchers {
		currentWatchers[tableName] = true
	}
	m.mu.RUnlock()

	// Start watchers for new tables
	for tableName := range enabledSet {
		if !currentWatchers[tableName] {
			table, err := m.tableStore.Get(ctx, tableName)
			if err != nil {
				log.Printf("Failed to get table %s: %v", tableName, err)
				continue
			}
			if err := m.startWatcher(ctx, *table); err != nil {
				log.Printf("Failed to start watcher for %s: %v", tableName, err)
			}
		}
	}

	// Stop watchers for disabled tables
	for tableName := range currentWatchers {
		if !enabledSet[tableName] {
			if err := m.stopWatcher(ctx, tableName); err != nil {
				log.Printf("Failed to stop watcher for %s: %v", tableName, err)
			}
		}
	}

	log.Printf("Sync complete: %d active watchers", len(enabledSet))
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

// waitForReplicaSet waits until the replica set is ready
func (m *Manager) waitForReplicaSet(ctx context.Context) error {
	log.Println("Waiting for replica set to be ready...")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(60 * time.Second)
	attempts := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for replica set")
		case <-ticker.C:
			attempts++
			err := m.checkReplicaSet(ctx)
			if err == nil {
				log.Printf("Replica set is ready (attempt %d)", attempts)
				return nil
			}
			log.Printf("Replica set not ready yet (attempt %d): %v", attempts, err)
		}
	}
}

// checkReplicaSet checks if the replica set is initialized and ready
func (m *Manager) checkReplicaSet(ctx context.Context) error {
	cmd := bson.D{{Key: "replSetGetStatus", Value: 1}}
	result := m.mongoClient.Database("admin").RunCommand(ctx, cmd)

	var status struct {
		Ok     int `bson:"ok"`
		Set    string `bson:"set"`
		Members []struct {
			Name   string `bson:"name"`
			State  int    `bson:"state"`
			StateStr string `bson:"stateStr"`
		} `bson:"members"`
	}

	if err := result.Decode(&status); err != nil {
		return err
	}

	if status.Ok != 1 {
		return fmt.Errorf("replica set status not ok")
	}

	if len(status.Members) == 0 {
		return fmt.Errorf("no replica set members")
	}

	// Check if at least one member is PRIMARY (state 1)
	for _, member := range status.Members {
		if member.State == 1 { // PRIMARY
			return nil
		}
	}

	return fmt.Errorf("no primary member found")
}
