package collections

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// newTestDLQManager connects to MongoDB and returns a Manager whose DLQ is
// backed by a dedicated test database. It skips the test if MongoDB is not
// available.
func newTestDLQManager(t *testing.T) (*Manager, *mongo.Client, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(localMongoURI))
	if err != nil {
		t.Skipf("MongoDB not available: %v", err)
	}
	t.Cleanup(func() { client.Disconnect(context.Background()) })

	// Drop any leftover state from a previous run so the test is idempotent.
	require.NoError(t, client.Database("conduit_test_dlq").Drop(ctx))

	manager := NewManager(client, "conduit_test_dlq")
	require.NoError(t, manager.CreateIndex(ctx))
	return manager, client, ctx
}

func TestManagerDLQPersistAndRead(t *testing.T) {
	manager, _, ctx := newTestDLQManager(t)

	// The DLQ read methods validate the collection is managed, so register them.
	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: "users"}))
	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: "orders"}))

	entry := DLQEntry{
		CollectionName: "users",
		EventData:      []byte(`{"tableName":"users"}`),
		FailedAt:       time.Now(),
		DedupKey:       "users:event-1",
	}

	t.Run("persist then list by collection", func(t *testing.T) {
		require.NoError(t, manager.CreateDLQEntry(ctx, entry))

		entries, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "users", entries[0].CollectionName)
		assert.Equal(t, "users:event-1", entries[0].DedupKey)
	})

	t.Run("persist is idempotent on dedup key", func(t *testing.T) {
		// Persisting the same dedup key again must not create a duplicate.
		require.NoError(t, manager.CreateDLQEntry(ctx, entry))

		entries, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, entries, 1, "duplicate dedup key must not create a second entry")
	})

	t.Run("get by id returns the entry", func(t *testing.T) {
		entries, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, entries, 1)

		got, err := manager.GetDLQEntry(ctx, "users", entries[0].ID.Hex())
		require.NoError(t, err)
		assert.Equal(t, entries[0].ID, got.ID)
		assert.Equal(t, "users", got.CollectionName)
	})

	t.Run("get by id from another collection returns not found", func(t *testing.T) {
		entries, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, entries, 1)

		_, err = manager.GetDLQEntry(ctx, "orders", entries[0].ID.Hex())
		require.ErrorIs(t, err, ErrDLQEntryNotFound)
	})

	t.Run("get by unknown id returns not found", func(t *testing.T) {
		_, err := manager.GetDLQEntry(ctx, "users", primitive.NewObjectID().Hex())
		require.ErrorIs(t, err, ErrDLQEntryNotFound)
	})

	t.Run("get by malformed id returns not found", func(t *testing.T) {
		_, err := manager.GetDLQEntry(ctx, "users", "not-an-objectid")
		require.ErrorIs(t, err, ErrDLQEntryNotFound)
	})

	t.Run("list is scoped to collection", func(t *testing.T) {
		other := DLQEntry{
			CollectionName: "orders",
			EventData:      []byte(`{"tableName":"orders"}`),
			FailedAt:       time.Now(),
			DedupKey:       "orders:event-1",
		}
		require.NoError(t, manager.CreateDLQEntry(ctx, other))

		users, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, users, 1)
		assert.Equal(t, "users", users[0].CollectionName)

		orders, err := manager.ListDLQEntries(ctx, "orders", DLQListOptions{})
		require.NoError(t, err)
		require.Len(t, orders, 1)
		assert.Equal(t, "orders", orders[0].CollectionName)
	})
}

func TestManagerDLQListPagination(t *testing.T) {
	manager, _, ctx := newTestDLQManager(t)
	require.NoError(t, manager.Create(ctx, &Collection{CollectionName: "users"}))

	// Seed several entries for the same collection.
	for i := 0; i < 5; i++ {
		require.NoError(t, manager.CreateDLQEntry(ctx, DLQEntry{
			CollectionName: "users",
			EventData:      []byte(`{"tableName":"users"}`),
			FailedAt:       time.Now().Add(time.Duration(i) * time.Second),
			DedupKey:       "users:event-" + string(rune('a'+i)),
		}))
	}

	t.Run("limit bounds the result set", func(t *testing.T) {
		entries, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{Limit: 2})
		require.NoError(t, err)
		require.Len(t, entries, 2)
	})

	t.Run("skip advances the page", func(t *testing.T) {
		first, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{Limit: 2})
		require.NoError(t, err)
		second, err := manager.ListDLQEntries(ctx, "users", DLQListOptions{Limit: 2, Skip: 2})
		require.NoError(t, err)
		require.Len(t, second, 2)
		// Deterministic failedAt-descending sort: pages must not overlap.
		assert.NotEqual(t, first[0].ID, second[0].ID)
	})

	t.Run("empty result set is non-nil", func(t *testing.T) {
		require.NoError(t, manager.Create(ctx, &Collection{CollectionName: "nonexistent"}))
		entries, err := manager.ListDLQEntries(ctx, "nonexistent", DLQListOptions{})
		require.NoError(t, err)
		assert.NotNil(t, entries)
		assert.Empty(t, entries)
	})
}

func TestManagerDLQUnmanagedCollection(t *testing.T) {
	manager, _, ctx := newTestDLQManager(t)

	// A physical collection that is not registered in config.collections must
	// never be readable through the DLQ methods.
	t.Run("list rejects unmanaged collection", func(t *testing.T) {
		_, err := manager.ListDLQEntries(ctx, "unregistered", DLQListOptions{})
		require.ErrorIs(t, err, ErrCollectionNotFound)
	})

	t.Run("get rejects unmanaged collection", func(t *testing.T) {
		_, err := manager.GetDLQEntry(ctx, "unregistered", primitive.NewObjectID().Hex())
		require.ErrorIs(t, err, ErrCollectionNotFound)
	})

	t.Run("count rejects unmanaged collection", func(t *testing.T) {
		_, err := manager.CountDLQEntries(ctx, "unregistered")
		require.ErrorIs(t, err, ErrCollectionNotFound)
	})
}
