package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
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
	defer m.mu.Unlock()

	watcher, exists := m.watchers[collectionName]
	if !exists {
		return nil
	}

	log.Printf("Stopping watcher for collection: %s", collectionName)

	if err := watcher.Stop(ctx); err != nil {
		return err
	}

	delete(m.watchers, collectionName)
	delete(m.currentDestinations, collectionName)

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
			// Only refresh destinations for the changed collection, don't full sync
			// (syncLoop will handle watcher start/stop on next interval)
			m.refreshDestinations(ctx, collectionName)
		}
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
	toAdd, toRemove := diffDestinations(current, desired)

	for _, dest := range toRemove {
		name := destinationName(dest)
		d.Remove(collectionName, name)
		log.Printf("Removed destination %s for collection %s", name, collectionName)
	}

	for _, dest := range toAdd {
		m.registerSingleDestination(ctx, collectionName, dest, d)
	}

	m.mu.Lock()
	m.currentDestinations[collectionName] = desired
	m.mu.Unlock()

	log.Printf("Refreshed destinations for collection %s (%d added, %d removed)", collectionName, len(toAdd), len(toRemove))
	return nil
}

// registerSingleDestination creates and registers a single destination
func (m *Manager) registerSingleDestination(ctx context.Context, collectionName string, dest collections.DestinationConfig, d *dispatch.Dispatcher) {
	switch dest.Type {
	case "http":
		if dest.Endpoint == "" {
			log.Printf("HTTP destination requested but endpoint not set for collection %s", collectionName)
			return
		}
		eventTypes := dest.EventTypes
		if len(eventTypes) == 0 {
			eventTypes = []string{"INSERT", "MODIFY", "REMOVE"}
		}
		httpDest, err := dispatch.NewHTTPDestination(dest.Endpoint, dest.BearerToken, eventTypes, dest.FilterCriteria)
		if err != nil {
			log.Printf("Failed to create HTTP destination for %s: %v", collectionName, err)
			return
		}
		d.Register(collectionName, httpDest)
		log.Printf("Registered HTTP destination for collection %s -> %s", collectionName, dest.Endpoint)
	case "eventbridge":
		region := dest.Region
		if region == "" {
			log.Printf("EventBridge destination for %s missing required 'region'", collectionName)
			return
		}
		busName := dest.EventBusName
		if busName == "" {
			busName = dest.Endpoint
		}
		if busName == "" {
			log.Printf("EventBridge destination for %s missing required 'event_bus_name' or 'endpoint'", collectionName)
			return
		}
		ebDest, err := dispatch.NewEventBridgeDestination(region, busName, dest.Source, "")
		if err != nil {
			log.Printf("Failed to create EventBridge destination for %s: %v", collectionName, err)
			return
		}
		d.Register(collectionName, ebDest)
		log.Printf("Registered EventBridge destination for collection %s -> %s@%s", collectionName, busName, region)
	case "meilisearch":
		host := dest.Endpoint
		if host == "" {
			log.Printf("Meilisearch destination for %s missing required 'endpoint' (host)", collectionName)
			return
		}
		indexName := dest.IndexName
		if indexName == "" {
			indexName = collectionName
		}
		meiliDest, err := dispatch.NewMeilisearchDestination(host, dest.BearerToken, indexName)
		if err != nil {
			log.Printf("Failed to create Meilisearch destination for %s: %v", collectionName, err)
			return
		}
		d.Register(collectionName, meiliDest)
		log.Printf("Registered Meilisearch destination for collection %s -> %s/%s", collectionName, host, indexName)
	default:
		log.Printf("Unknown destination type: %s for collection %s", dest.Type, collectionName)
	}
}

// registerDestinations registers event destinations for a collection
func (m *Manager) registerDestinations(ctx context.Context, collectionName string, destinations []collections.DestinationConfig) error {
	for _, dest := range destinations {
		switch dest.Type {
		case "http":
			if dest.Endpoint == "" {
				log.Printf("HTTP destination requested but endpoint not set for collection %s", collectionName)
				continue
			}
			// Default to all event types if not specified
			eventTypes := dest.EventTypes
			if len(eventTypes) == 0 {
				eventTypes = []string{"INSERT", "MODIFY", "REMOVE"}
			}
			httpDest, err := dispatch.NewHTTPDestination(dest.Endpoint, dest.BearerToken, eventTypes, dest.FilterCriteria)
			if err != nil {
				log.Printf("Failed to create HTTP destination for %s: %v", collectionName, err)
				continue
			}
			// Cast to Dispatcher interface to access Register method
			if d, ok := m.dispatcher.(*dispatch.Dispatcher); ok {
				d.Register(collectionName, httpDest)
				log.Printf("Registered HTTP destination for collection %s -> %s", collectionName, dest.Endpoint)
			}
		case "eventbridge":
			region := dest.Region
			if region == "" {
				log.Printf("EventBridge destination for %s missing required 'region'", collectionName)
				continue
			}
			busName := dest.EventBusName
			if busName == "" {
				busName = dest.Endpoint
			}
			if busName == "" {
				log.Printf("EventBridge destination for %s missing required 'event_bus_name' or 'endpoint'", collectionName)
				continue
			}
			ebDest, err := dispatch.NewEventBridgeDestination(region, busName, dest.Source, "")
			if err != nil {
				log.Printf("Failed to create EventBridge destination for %s: %v", collectionName, err)
				continue
			}
			if d, ok := m.dispatcher.(*dispatch.Dispatcher); ok {
				d.Register(collectionName, ebDest)
				log.Printf("Registered EventBridge destination for collection %s -> %s@%s", collectionName, busName, region)
			}
		case "meilisearch":
			host := dest.Endpoint
			if host == "" {
				log.Printf("Meilisearch destination for %s missing required 'endpoint' (host)", collectionName)
				continue
			}
			indexName := dest.IndexName
			if indexName == "" {
				indexName = collectionName
			}
			meiliDest, err := dispatch.NewMeilisearchDestination(host, dest.BearerToken, indexName)
			if err != nil {
				log.Printf("Failed to create Meilisearch destination for %s: %v", collectionName, err)
				continue
			}
			if d, ok := m.dispatcher.(*dispatch.Dispatcher); ok {
				d.Register(collectionName, meiliDest)
				log.Printf("Registered Meilisearch destination for collection %s -> %s/%s", collectionName, host, indexName)
			}
		default:
			log.Printf("Unknown destination type: %s for collection %s", dest.Type, collectionName)
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
// It includes type-specific identifiers so that two destinations of the same
// type but with different configs are treated as distinct.
func destinationName(dest collections.DestinationConfig) string {
	switch dest.Type {
	case "eventbridge":
		return dest.Type + ":" + dest.Region + ":" + dest.EventBusName
	case "meilisearch":
		return dest.Type + ":" + dest.Endpoint + ":" + dest.IndexName
	default:
		return dest.Type + ":" + dest.Endpoint
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

func filterConditionEqual(a, b collections.FilterCondition) bool {
	if !ptrStrEqual(a.Prefix, b.Prefix) || !ptrStrEqual(a.Suffix, b.Suffix) || !ptrBoolEqual(a.Exists, b.Exists) {
		return false
	}
	if !reflect.DeepEqual(a.Numeric, b.Numeric) || !reflect.DeepEqual(a.AnythingBut, b.AnythingBut) {
		return false
	}
	return true
}

func ptrStrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func ptrBoolEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func imageFilterEqual(a, b collections.ImageFilter) bool {
	if len(a) != len(b) {
		return false
	}
	for field, condA := range a {
		condB, ok := b[field]
		if !ok {
			return false
		}
		if !filterConditionEqual(condA, condB) {
			return false
		}
	}
	return true
}
