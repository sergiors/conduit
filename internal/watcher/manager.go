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
	"github.com/sergiors/conduit/internal/recover"
	redisclient "github.com/sergiors/conduit/internal/redis"
	"github.com/sergiors/conduit/internal/retry"
	"github.com/sergiors/conduit/internal/streams"
	"go.mongodb.org/mongo-driver/mongo"
)

// Manager manages CDC watchers for all enabled collections
type Manager struct {
	mongoClient        *mongo.Client
	database           string
	collectionSettings *collections.Settings
	redisClient        *redisclient.Client
	dispatcher         Dispatcher
	retryProcessor     *retry.Processor
	watchers           map[string]*Watcher
	currentSinks       map[string][]collections.Sink
	mu                 sync.RWMutex
	syncInterval       time.Duration
	pubsub             *redis.PubSub
	configChan         <-chan *redis.Message

	// Runtime state
	runCtx    context.Context
	runCancel context.CancelFunc
	wg        sync.WaitGroup
	stopped   bool
	startMu   sync.Mutex
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
	collectionSettings *collections.Settings,
	redisClient *redisclient.Client,
	dispatcher Dispatcher,
	retryProcessor *retry.Processor,
	cfg Config,
) *Manager {
	return &Manager{
		mongoClient:        mongoClient,
		database:           database,
		collectionSettings: collectionSettings,
		redisClient:        redisClient,
		dispatcher:         dispatcher,
		retryProcessor:     retryProcessor,
		watchers:           make(map[string]*Watcher),
		currentSinks:       make(map[string][]collections.Sink),
		syncInterval:       cfg.SyncInterval,
	}
}

// Start initializes and starts all watchers
func (m *Manager) Start(ctx context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	if m.stopped {
		return fmt.Errorf("manager already stopped")
	}

	log.Printf("Watcher manager starting with syncInterval=%s", m.syncInterval)

	// Derive a cancellable context owned by the manager so Stop can cancel the
	// sync/config-change loops independently of the parent context.
	m.runCtx, m.runCancel = context.WithCancel(ctx)

	// Initial load of stream-enabled collections
	collections, err := m.collectionSettings.ListStreamEnabled(ctx)
	if err != nil {
		return fmt.Errorf("load collections: %w", err)
	}

	log.Printf("Found %d stream-enabled collections", len(collections))

	// Start watcher for each enabled collection
	for _, collection := range collections {
		if err := m.startWatcher(m.runCtx, collection); err != nil {
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
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			recover.Protect("manager:configChange", func() {
				m.configChangeLoop(m.runCtx)
			})
		}()
	}

	// Start sync loop
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		recover.Protect("manager:sync", func() {
			m.syncLoop(m.runCtx)
		})
	}()

	return nil
}

// Stop stops all watchers and the manager's background loops. It cancels the
// manager's own context, waits for the sync/config-change loops to exit, stops
// every watcher, and closes the pub/sub subscription. Stop is idempotent:
// calling it more than once (or before Start) is a no-op and returns nil.
func (m *Manager) Stop(ctx context.Context) error {
	m.startMu.Lock()
	if m.stopped {
		m.startMu.Unlock()
		return nil
	}
	m.stopped = true
	if m.runCancel != nil {
		m.runCancel()
	}
	m.startMu.Unlock()

	log.Println("Watcher manager stopping...")

	var lastErr error

	// Bounded by the caller's context with a fallback timeout so a stuck loop
	// cannot hang shutdown.
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		lastErr = ctx.Err()
	case <-time.After(10 * time.Second):
		lastErr = fmt.Errorf("manager loop stop timeout")
	}

	m.mu.Lock()
	for collectionName, watcher := range m.watchers {
		if err := watcher.Stop(ctx); err != nil {
			log.Printf("Failed to stop watcher for %s: %v", collectionName, err)
			lastErr = err
		}
		delete(m.watchers, collectionName)
	}
	m.mu.Unlock()

	// Close Pub/Sub subscription
	if m.pubsub != nil {
		if err := m.pubsub.Close(); err != nil {
			log.Printf("Failed to close Pub/Sub: %v", err)
			lastErr = err
		}
	}

	return lastErr
}

// startWatcher creates and starts a watcher for a collection.
//
// The watcher's context (and the handleEvent closure it invokes) derives from
// m.runCtx, not the caller's ctx: cancelling the manager's run context must
// tear down any watcher, including one created concurrently by a
// configChangeLoop that was mid-flight when Stop began. startWatcher refuses
// to run once m.runCtx is cancelled, so Stop can never be raced into
// registering a watcher it will never drain.
func (m *Manager) startWatcher(ctx context.Context, collection collections.Collection) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.runCtx == nil || m.runCtx.Err() != nil {
		return fmt.Errorf("watcher start refused: manager is stopping or stopped")
	}

	// Check if already running
	if _, exists := m.watchers[collection.CollectionName]; exists {
		log.Printf("Watcher for %s already running", collection.CollectionName)
		return nil
	}

	log.Printf("Starting watcher for collection: %s", collection.CollectionName)

	// Register sinks for this collection
	var sinkConfigs []collections.Sink
	if m.collectionSettings != nil {
		var err error
		sinkConfigs, err = m.loadSinks(ctx, collection.CollectionName)
		if err != nil {
			log.Printf("Failed to load sinks for %s: %v", collection.CollectionName, err)
			sinkConfigs = nil
		}
	}
	if err := m.registerSinks(ctx, collection.CollectionName, sinkConfigs); err != nil {
		log.Printf("Failed to register sinks for %s: %v", collection.CollectionName, err)
	}
	m.currentSinks[collection.CollectionName] = sinkConfigs

	// Get resume token from Redis
	var resumeToken string
	if m.redisClient != nil {
		var err error
		resumeToken, err = m.redisClient.GetResumeToken(ctx, collection.CollectionName)
		if err != nil {
			log.Printf("Failed to get resume token for %s: %v", collection.CollectionName, err)
		}
	}

	// Create watcher
	watcher := NewWatcher(
		m.mongoClient,
		m.database,
		collection.CollectionName,
		collection.OldImage,
		resumeToken,
		m.redisClient,
	)

	// Start watcher with event handler. The watcher and its handler derive from
	// m.runCtx so Stop's runCancel() tears them down (see the doc comment above).
	if err := watcher.Start(m.runCtx, func(record streams.StreamRecord) error {
		return m.handleEvent(m.runCtx, collection.CollectionName, record)
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
	// The event ID is derived deterministically from the MongoDB change event
	// (resume token / clusterTime + documentKey) by the watcher, so the same
	// change always maps to the same idempotency key across restarts.
	if record.EventID == "" {
		return fmt.Errorf("event is missing a deterministic event ID")
	}
	eventID := record.EventID

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
	// Use 24h TTL for idempotency key.
	//
	// The dispatch already succeeded, so this write must survive a shutdown
	// that cancels the live ctx mid-event; losing it would cause a duplicate
	// delivery on restart.
	bkctx, bkCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer bkCancel()
	if err := m.redisClient.MarkProcessed(bkctx, eventID, 24*time.Hour); err != nil {
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

	// Terminal bookkeeping write: the dispatch already failed, so if this
	// enqueue is lost to a cancelled ctx the event is silently gone.
	bkctx, bkCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer bkCancel()
	return m.redisClient.EnqueueRetry(bkctx, retryEvent)
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
			// Handle change for specific collection. A panic while handling
			// one message must not kill the loop; the next config change or
			// sync will retry.
			if _, panicked := recover.ProtectErr("manager:configChange", func() error {
				m.handleCollectionChange(ctx, collectionName)
				return nil
			}); panicked {
				log.Printf("Config change handling for %s panicked; continuing loop", collectionName)
			}
		}
	}
}

// handleCollectionChange handles config changes for a single collection
func (m *Manager) handleCollectionChange(ctx context.Context, collectionName string) {
	collection, err := m.collectionSettings.Get(ctx, collectionName)
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
			// A panic while syncing must not kill the loop; the next tick
			// retries the reconciliation.
			if _, panicked := recover.ProtectErr("manager:sync", func() error {
				m.syncWithCollections(ctx)
				return nil
			}); panicked {
				log.Println("Sync panicked; continuing loop")
			}
		}
	}
}

// syncWithCollections diffs current watchers with config.collections
func (m *Manager) syncWithCollections(ctx context.Context) {
	log.Println("Syncing watchers with config.collections...")

	// Fetch current stream-enabled collections
	collectionList, err := m.collectionSettings.ListStreamEnabled(ctx)
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

// refreshSinks reconciles current vs desired sinks and only applies changes.
func (m *Manager) refreshSinks(ctx context.Context, collectionName string) error {
	d, ok := m.dispatcher.(*dispatch.Dispatcher)
	if !ok {
		return nil
	}

	desired, err := m.loadSinks(ctx, collectionName)
	if err != nil {
		log.Printf("Failed to load sinks for %s: %v", collectionName, err)
		return err
	}

	m.mu.RLock()
	current := m.currentSinks[collectionName]
	m.mu.RUnlock()

	reconciliation := ReconcileSinks(current, desired)
	reconciliation.LogChanges(collectionName)
	reconciliation.ApplyChanges(ctx, collectionName, d)

	m.mu.Lock()
	m.currentSinks[collectionName] = desired
	m.mu.Unlock()

	if len(reconciliation.Changes) > 0 {
		log.Printf("Refreshed sinks for collection %s: %s", collectionName, reconciliation.Summary())
	}

	return nil
}

// loadSinks loads the persisted sinks for a collection by name.
func (m *Manager) loadSinks(ctx context.Context, collectionName string) ([]collections.Sink, error) {
	return m.collectionSettings.GetSinks(ctx, collectionName)
}

// registerSinks registers event sinks for a collection.
func (m *Manager) registerSinks(ctx context.Context, collectionName string, sinks []collections.Sink) error {
	d, ok := m.dispatcher.(*dispatch.Dispatcher)
	if !ok {
		return nil
	}

	for _, sink := range sinks {
		transport := dispatch.BuildTransport(ctx, collectionName, sink.Type, sink.Spec)
		if transport == nil {
			log.Printf("Failed to build transport for sink type %s (collection %s, sink %s); skipping registration", sink.Type, collectionName, sink.ID)
			continue
		}
		runtimeSink := dispatch.NewRuntimeSink(sink, transport)
		d.Register(collectionName, runtimeSink)
		log.Printf("Registered %s sink for collection %s", sink.Type, collectionName)
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
