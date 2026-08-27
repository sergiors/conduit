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
	mongoClient     *mongo.Client
	database        string
	collectionStore *collections.Store
	redisClient     *redisclient.Client
	dispatcher      Dispatcher
	retryProcessor  *retry.Processor
	watchers        map[string]*Watcher
	currentSinks    map[string][]collections.SinkConfig
	mu              sync.RWMutex
	syncInterval    time.Duration
	pubsub          *redis.PubSub
	configChan      <-chan *redis.Message
}

// Dispatcher interface for decoupling
type Dispatcher interface {
	Dispatch(ctx context.Context, collectioName string, record streams.StreamRecord) error
}

// Config holds watcher manager configuration
type Config struct {
	SyncInterval time.Duration // How often to sync with config.collections
	HTTPEndpoint string        // HTTP endpoint for creating sinks
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
		mongoClient:     mongoClient,
		database:        database,
		collectionStore: collectionStore,
		redisClient:     redisClient,
		dispatcher:      dispatcher,
		retryProcessor:  retryProcessor,
		watchers:        make(map[string]*Watcher),
		currentSinks:    make(map[string][]collections.SinkConfig),
		syncInterval:    cfg.SyncInterval,
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

	// Register sinks for this collection
	sinkConfigs, err := m.loadSinkConfigs(ctx, collection.CollectionName)
	if err != nil {
		log.Printf("Failed to load sinks for %s: %v", collection.CollectionName, err)
		sinkConfigs = nil
	}
	if err := m.registerSinks(ctx, collection.CollectionName, sinkConfigs); err != nil {
		log.Printf("Failed to register sinks for %s: %v", collection.CollectionName, err)
	}
	m.currentSinks[collection.CollectionName] = sinkConfigs

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
	delete(m.currentSinks, collectionName)
	m.mu.Unlock()

	if err := watcher.Stop(ctx); err != nil {
		return err
	}

	// Clear sinks from dispatcher
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

	// Dispatch to sinks
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
		// New collection - start watcher with sinks
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

	// Always refresh sinks for existing watchers
	if err := m.refreshSinks(ctx, collectionName); err != nil {
		log.Printf("Failed to refresh sinks for %s: %v", collectionName, err)
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

	// Start watchers for new collections and register sinks
	for collectionName, collection := range enabledSet {
		existingWatcher, exists := currentWatchers[collectionName]
		if !exists {
			// New collection - start watcher (which also registers sinks)
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
			// Always refresh sinks for existing collections
			if err := m.refreshSinks(ctx, collectionName); err != nil {
				log.Printf("Failed to refresh sinks for %s: %v", collectionName, err)
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

// refreshSinks diffs current vs desired sinks and only updates what changed
func (m *Manager) refreshSinks(ctx context.Context, collectionName string) error {
	d, ok := m.dispatcher.(*dispatch.Dispatcher)
	if !ok {
		return nil
	}

	desired, err := m.loadSinkConfigs(ctx, collectionName)
	if err != nil {
		log.Printf("Failed to load sinks for %s: %v", collectionName, err)
		return err
	}

	m.mu.RLock()
	current := m.currentSinks[collectionName]
	m.mu.RUnlock()

	// If desired is empty, remove all current sinks
	if len(desired) == 0 {
		diff := DiffSinks(current, desired)
		diff.LogChanges(collectionName)
		diff.ApplyChanges(ctx, collectionName, d)

		m.mu.Lock()
		delete(m.currentSinks, collectionName)
		m.mu.Unlock()

		if len(diff.Changes) > 0 {
			log.Printf("Removed all sinks for collection %s", collectionName)
		}
		return nil
	}

	// Compute diff and apply changes
	diff := DiffSinks(current, desired)
	diff.LogChanges(collectionName)
	diff.ApplyChanges(ctx, collectionName, d)

	// Update state
	m.mu.Lock()
	m.currentSinks[collectionName] = desired
	m.mu.Unlock()

	if len(diff.Changes) > 0 {
		log.Printf("Refreshed sinks for collection %s: %s", collectionName, diff.Summary())
	}

	return nil
}

// loadSinkConfigs loads the sink configs for a collection by name.
func (m *Manager) loadSinkConfigs(ctx context.Context, collectionName string) ([]collections.SinkConfig, error) {
	sinks, err := m.collectionStore.GetSinks(ctx, collectionName)
	if err != nil {
		return nil, err
	}
	configs := make([]collections.SinkConfig, 0, len(sinks))
	for _, sink := range sinks {
		configs = append(configs, sink.SinkConfig)
	}
	return configs, nil
}

// registerSinks registers event sinks for a collection
func (m *Manager) registerSinks(ctx context.Context, collectionName string, sinks []collections.SinkConfig) error {
	d, ok := m.dispatcher.(*dispatch.Dispatcher)
	if !ok {
		return nil
	}

	for _, dest := range sinks {
		if created := dispatch.BuildSink(ctx, collectionName, dest); created != nil {
			d.Register(collectionName, created)
			log.Printf("Registered %s sink for collection %s", dest.Type, collectionName)
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

// diffSinks compares current and desired sink configs, returning which to add/remove
func diffSinks(current, desired []collections.SinkConfig) (toAdd, toRemove []collections.SinkConfig) {
	currentByKey := make(map[string]collections.SinkConfig, len(current))
	for _, c := range current {
		currentByKey[sinkName(c)] = c
	}

	desiredByKey := make(map[string]collections.SinkConfig, len(desired))
	for _, d := range desired {
		desiredByKey[sinkName(d)] = d
	}

	// Find sinks to remove: in current but not in desired, or config changed
	for key, cur := range currentByKey {
		des, exists := desiredByKey[key]
		if !exists || !configEqual(cur, des) {
			toRemove = append(toRemove, cur)
		}
	}

	// Find sinks to add: in desired but not in current, or config changed
	for key, des := range desiredByKey {
		cur, exists := currentByKey[key]
		if !exists || !configEqual(cur, des) {
			toAdd = append(toAdd, des)
		}
	}

	return
}

// sinkName builds the internal name for a sink config.
// This MUST match exactly what Sink.Name() returns for Remove() to work correctly.
func sinkName(dest collections.SinkConfig) string {
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

// configEqual compares two sink configs ignoring event type order.
// It checks both common fields and type-specific fields.
func configEqual(a, b collections.SinkConfig) bool {
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
