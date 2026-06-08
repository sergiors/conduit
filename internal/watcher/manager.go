package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sergiors/conduit/internal/collections"
	"github.com/sergiors/conduit/internal/dispatch"
	redisclient "github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/retry"
	"github.com/sergiors/conduit/internal/streams"
	"go.mongodb.org/mongo-driver/mongo"
)

// Manager manages CDC watchers for all enabled collections
type Manager struct {
	mongoClient         *mongo.Client
	database            string
	collectionStore     *collections.Store
	redisClient         *redisclient.Client
	dispatcher          Dispatcher
	retryProcessor      *retry.Processor
	watchers            map[string]*Watcher
	currentDestinations map[string][]collections.DestinationConfig
	mu                  sync.RWMutex
	syncInterval        time.Duration
	pubsub              *redis.PubSub
	configChan          <-chan *redis.Message
}

// Dispatcher interface for decoupling
type Dispatcher interface {
	Dispatch(ctx context.Context, collectioName string, record streams.StreamRecord) error
}

// Config holds watcher manager configuration
type Config struct {
	SyncInterval time.Duration // How often to sync with config.collections
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
	collectionStore *collections.Store,
	redisClient *redisclient.Client,
	dispatcher Dispatcher,
	retryProcessor *retry.Processor,
	cfg Config,
) *Manager {
	return &Manager{
		mongoClient:         mongoClient,
		database:            database,
		collectionStore:     collectionStore,
		redisClient:         redisClient,
		dispatcher:          dispatcher,
		retryProcessor:      retryProcessor,
		watchers:            make(map[string]*Watcher),
		currentDestinations: make(map[string][]collections.DestinationConfig),
		syncInterval:        cfg.SyncInterval,
	}
}

// Start initializes and starts all watchers
func (m *Manager) Start(ctx context.Context) error {
	log.Println("Watcher manager starting...")

	// Initial load of stream-enabled collections
	collections, err := m.collectionStore.ListStreamEnabled(ctx)
	if err != nil {
		return fmt.Errorf("load collections: %w", err)
	}

	log.Printf("Found %d stream-enabled collections", len(collections))

	// Start watcher for each enabled collection
	for _, collection := range collections {
		if err := m.startWatcher(ctx, collection); err != nil {
			log.Printf("Failed to start watcher for %s: %v", collection.CollectionName, err)
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
	for collectionName, watcher := range m.watchers {
		if err := watcher.Stop(ctx); err != nil {
			log.Printf("Failed to stop watcher for %s: %v", collectionName, err)
			lastErr = err
		}
		delete(m.watchers, collectionName)
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

// startWatcher creates and starts a watcher for a collection
func (m *Manager) startWatcher(ctx context.Context, collection collections.Collection) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if _, exists := m.watchers[collection.CollectionName]; exists {
		log.Printf("Watcher for %s already running", collection.CollectionName)
		return nil
	}

	log.Printf("Starting watcher for collection: %s", collection.CollectionName)

	// Register destinations for this collection
	if err := m.registerDestinations(ctx, collection.CollectionName, collection.Destinations); err != nil {
		log.Printf("Failed to register destinations for %s: %v", collection.CollectionName, err)
	}
	m.currentDestinations[collection.CollectionName] = collection.Destinations

	// Get resume token from Redis
	resumeToken, err := m.redisClient.GetResumeToken(ctx, collection.CollectionName)
	if err != nil {
		log.Printf("Failed to get resume token for %s: %v", collection.CollectionName, err)
	}

	// Create watcher
	watcher := NewWatcher(
		m.mongoClient,
		m.database,
		collection.CollectionName,
		collection.PartitionKey,
		collection.SortKey,
		collection.OldImage,
		resumeToken,
		m.redisClient,
	)

	// Start watcher with event handler
	if err := watcher.Start(ctx, func(record streams.StreamRecord) error {
		return m.handleEvent(ctx, collection.CollectionName, record)
	}); err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}

	m.watchers[collection.CollectionName] = watcher

	// Register collection with retry processor
	if m.retryProcessor != nil {
		m.retryProcessor.RegisterCollection(collection.CollectionName)
	}

	return nil
}

// stopWatcher stops and removes a watcher
func (m *Manager) stopWatcher(ctx context.Context, collectionName string) error {
	m.mu.Lock()
	watcher, exists := m.watchers[collectionName]
	if !exists {
		m.mu.Unlock()
		return nil // Already stopped (idempotent)
	}

	// Remove from map BEFORE stopping to prevent duplicate stop attempts
	delete(m.watchers, collectionName)
	delete(m.currentDestinations, collectionName)
	m.mu.Unlock()

	if err := watcher.Stop(ctx); err != nil {
		return err
	}

	// Clear destinations from dispatcher
	if d, ok := m.dispatcher.(*dispatch.Dispatcher); ok {
		d.Clear(collectionName)
	}

	// Unregister collection from retry processor
	if m.retryProcessor != nil {
		m.retryProcessor.UnregisterCollection(collectionName)
	}

	return nil
}

// restartWatcher stops and restarts a watcher with updated config
func (m *Manager) restartWatcher(ctx context.Context, collection collections.Collection) error {
	// Stop existing watcher
	if err := m.stopWatcher(ctx, collection.CollectionName); err != nil {
		return err
	}

	// Start new watcher with updated config
	return m.startWatcher(ctx, collection)
}

// handleEvent processes an event with idempotency and retry logic
func (m *Manager) handleEvent(ctx context.Context, collectionName string, record streams.StreamRecord) error {
	// Generate event ID: {collection}:{type}:{timestamp}
	eventID := fmt.Sprintf("%s:%s:%d", collectionName, record.RecordType, record.Timestamp.UnixNano())

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
	if err := m.dispatcher.Dispatch(ctx, collectionName, record); err != nil {
		log.Printf("Dispatch failed for %s: %v", eventID, err)
		// Queue for retry
		return m.queueRetry(ctx, collectionName, record, eventID)
	}

	// Mark as processed
	// Use 24h TTL for idempotency key
	if err := m.redisClient.MarkProcessed(ctx, eventID, 24*time.Hour); err != nil {
		log.Printf("Failed to mark event as processed: %v", err)
	}

	return nil
}

// queueRetry adds failed event to retry queue with exponential backoff
func (m *Manager) queueRetry(ctx context.Context, collectionName string, record streams.StreamRecord, eventID string) error {
	retryID := fmt.Sprintf("%s:%s", collectionName, eventID)

	// Marshal the stream record to JSON
	eventData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal stream record: %w", err)
	}

	retryEvent := redisclient.RetryEvent{
		ID:             retryID,
		CollectionName: collectionName,
		EventData:      eventData,
		RetryCount:     0,
		MaxRetries:     5,
		NextRetryAt:    time.Now().Add(time.Second), // First retry after 1s
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
			collectionName := msg.Payload
			log.Printf("Config change detected for collection: %s", collectionName)
			// Handle change for specific collection
			m.handleCollectionChange(ctx, collectionName)
		}
	}
}

// handleCollectionChange handles config changes for a single collection
func (m *Manager) handleCollectionChange(ctx context.Context, collectionName string) {
	collection, err := m.collectionStore.Get(ctx, collectionName)
	if err != nil {
		log.Printf("Failed to fetch collection %s for config change: %v", collectionName, err)
		return
	}

	m.mu.RLock()
	_, watcherExists := m.watchers[collectionName]
	m.mu.RUnlock()

	if !collection.StreamEnabled {
		// Collection disabled - stop watcher if running
		if watcherExists {
			log.Printf("Collection %s disabled, stopping watcher", collectionName)
			if err := m.stopWatcher(ctx, collectionName); err != nil {
				log.Printf("Failed to stop watcher for %s: %v", collectionName, err)
			}
		}
		return
	}

	// Collection enabled
	if !watcherExists {
		// New collection - start watcher with destinations
		log.Printf("Collection %s enabled, starting watcher", collectionName)
		if err := m.startWatcher(ctx, *collection); err != nil {
			log.Printf("Failed to start watcher for %s: %v", collectionName, err)
		}
		return
	}

	// Existing watcher - check if oldImage changed (requires restart)
	m.mu.RLock()
	watcher := m.watchers[collectionName]
	m.mu.RUnlock()

	if watcher.OldImage() != collection.OldImage {
		log.Printf("oldImage changed for %s, restarting watcher", collectionName)
		if err := m.restartWatcher(ctx, *collection); err != nil {
			log.Printf("Failed to restart watcher for %s: %v", collectionName, err)
		}
	}

	// Always refresh destinations for existing watchers
	if err := m.refreshDestinations(ctx, collectionName); err != nil {
		log.Printf("Failed to refresh destinations for %s: %v", collectionName, err)
	}
}

// syncLoop periodically syncs watchers with config.collections
func (m *Manager) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(m.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.syncWithCollections(ctx)
		}
	}
}

// syncWithCollections diffs current watchers with config.collections
func (m *Manager) syncWithCollections(ctx context.Context) {
	log.Println("Syncing watchers with config.collections...")

	// Fetch current stream-enabled collections
	collectionList, err := m.collectionStore.ListStreamEnabled(ctx)
	if err != nil {
		log.Printf("Failed to list collections: %v", err)
		return
	}

	// Build set of enabled collection names
	enabledSet := make(map[string]collections.Collection)
	for _, collection := range collectionList {
		enabledSet[collection.CollectionName] = collection
	}

	// Get current watchers
	m.mu.RLock()
	currentWatchers := make(map[string]*Watcher)
	for collectionName, watcher := range m.watchers {
		currentWatchers[collectionName] = watcher
	}
	m.mu.RUnlock()

	// Start watchers for new collections and register destinations
	for collectionName, collection := range enabledSet {
		existingWatcher, exists := currentWatchers[collectionName]
		if !exists {
			// New collection - register destinations and start watcher
			if err := m.registerDestinations(ctx, collectionName, collection.Destinations); err != nil {
				log.Printf("Failed to register destinations for %s: %v", collectionName, err)
			}

			if err := m.startWatcher(ctx, collection); err != nil {
				log.Printf("Failed to start watcher for %s: %v", collectionName, err)
			}
		} else {
			// Existing collection - check if oldImage config changed
			if existingWatcher.OldImage() != collection.OldImage {
				log.Printf("oldImage config changed for %s (old=%v, new=%v), restarting watcher", collectionName, existingWatcher.OldImage(), collection.OldImage)
				if err := m.restartWatcher(ctx, collection); err != nil {
					log.Printf("Failed to restart watcher for %s: %v", collectionName, err)
				}
			}
			// Always refresh destinations for existing collections
			if err := m.refreshDestinations(ctx, collectionName); err != nil {
				log.Printf("Failed to refresh destinations for %s: %v", collectionName, err)
			}
		}
	}

	// Stop watchers for disabled collections
	for collectionName := range currentWatchers {
		if _, exists := enabledSet[collectionName]; !exists {
			if err := m.stopWatcher(ctx, collectionName); err != nil {
				log.Printf("Failed to stop watcher for %s: %v", collectionName, err)
			}
		}
	}

	log.Printf("Sync complete: %d active watchers", len(enabledSet))
}

// refreshDestinations diffs current vs desired destinations and only updates what changed
func (m *Manager) refreshDestinations(ctx context.Context, collectionName string) error {
	collection, err := m.collectionStore.Get(ctx, collectionName)
	if err != nil {
		log.Printf("Failed to fetch collection %s for refresh: %v", collectionName, err)
		return err
	}

	d, ok := m.dispatcher.(*dispatch.Dispatcher)
	if !ok {
		return nil
	}

	m.mu.RLock()
	current := m.currentDestinations[collectionName]
	m.mu.RUnlock()

	desired := collection.Destinations

	// If desired is empty, remove all current destinations
	if len(desired) == 0 {
		diff := DiffDestinations(current, desired)
		diff.LogChanges(collectionName)
		diff.ApplyChanges(ctx, collectionName, d)

		m.mu.Lock()
		delete(m.currentDestinations, collectionName)
		m.mu.Unlock()

		if len(diff.Changes) > 0 {
			log.Printf("Removed all destinations for collection %s", collectionName)
		}
		return nil
	}

	// Compute diff and apply changes
	diff := DiffDestinations(current, desired)
	diff.LogChanges(collectionName)
	diff.ApplyChanges(ctx, collectionName, d)

	// Update state
	m.mu.Lock()
	m.currentDestinations[collectionName] = desired
	m.mu.Unlock()

	if len(diff.Changes) > 0 {
		log.Printf("Refreshed destinations for collection %s: %s", collectionName, diff.Summary())
	}

	return nil
}

// registerDestinations registers event destinations for a collection
func (m *Manager) registerDestinations(ctx context.Context, collectionName string, destinations []collections.DestinationConfig) error {
	d, ok := m.dispatcher.(*dispatch.Dispatcher)
	if !ok {
		return nil
	}

	for _, dest := range destinations {
		if created := dispatch.BuildDestination(ctx, collectionName, dest); created != nil {
			d.Register(collectionName, created)
			log.Printf("Registered %s destination for collection %s", dest.Type, collectionName)
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
func (m *Manager) GetWatcherStats(collectioName string) (WatcherStats, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	watcher, exists := m.watchers[collectioName]
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

// diffDestinations compares current and desired destination configs, returning which to add/remove
func diffDestinations(current, desired []collections.DestinationConfig) (toAdd, toRemove []collections.DestinationConfig) {
	currentByKey := make(map[string]collections.DestinationConfig, len(current))
	for _, c := range current {
		currentByKey[destinationName(c)] = c
	}

	desiredByKey := make(map[string]collections.DestinationConfig, len(desired))
	for _, d := range desired {
		desiredByKey[destinationName(d)] = d
	}

	// Find destinations to remove: in current but not in desired, or config changed
	for key, cur := range currentByKey {
		des, exists := desiredByKey[key]
		if !exists || !configEqual(cur, des) {
			toRemove = append(toRemove, cur)
		}
	}

	// Find destinations to add: in desired but not in current, or config changed
	for key, des := range desiredByKey {
		cur, exists := currentByKey[key]
		if !exists || !configEqual(cur, des) {
			toAdd = append(toAdd, des)
		}
	}

	return
}

// destinationName builds the internal name for a destination config.
// This MUST match exactly what Destination.Name() returns for Remove() to work correctly.
func destinationName(dest collections.DestinationConfig) string {
	switch dest.Type {
	case "eventbridge":
		// Matches: "eventbridge:" + eventBusName + "@" + region
		return "eventbridge:" + dest.EventBusName + "@" + dest.Region
	case "meilisearch":
		// Matches: "meilisearch:" + host + "/" + indexName
		return "meilisearch:" + dest.Endpoint + "/" + dest.IndexName
	default:
		// HTTP: just the endpoint
		return dest.Endpoint
	}
}

// configEqual compares two destination configs ignoring event type order.
// It checks both common fields and type-specific fields.
func configEqual(a, b collections.DestinationConfig) bool {
	if a.Type != b.Type || a.Endpoint != b.Endpoint || a.BearerToken != b.BearerToken {
		return false
	}
	if a.Region != b.Region || a.EventBusName != b.EventBusName || a.Source != b.Source || a.IndexName != b.IndexName {
		return false
	}
	if len(a.EventTypes) != len(b.EventTypes) {
		return false
	}
	aSet := make(map[string]bool, len(a.EventTypes))
	for _, et := range a.EventTypes {
		aSet[et] = true
	}
	for _, et := range b.EventTypes {
		if !aSet[et] {
			return false
		}
	}
	return imageFilterEqual(a.FilterCriteria.OldImage, b.FilterCriteria.OldImage) &&
		imageFilterEqual(a.FilterCriteria.NewImage, b.FilterCriteria.NewImage)
}

