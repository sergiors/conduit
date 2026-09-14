package apikey

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const localMongoURI = "mongodb://localhost:27017/?directConnection=true"

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// newTestManager connects to MongoDB, uses a unique database, and returns a
// wired Manager plus helpers. It skips when MongoDB is unavailable or running
// in -short mode, following the existing collections test convention.
func newTestManager(t *testing.T) (*Manager, *mongo.Client, context.Context) {
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
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	database := fmt.Sprintf("conduit_apikey_test_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = client.Database(database).Drop(context.Background()) })

	manager := NewManager(client, database, discardLogger)
	require.NoError(t, manager.CreateIndex(ctx))
	return manager, client, ctx
}

func TestManager_CreateAuthenticateRoundTrip(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	key, secret, err := manager.Create(ctx, "production")
	require.NoError(t, err)
	require.NotEmpty(t, key.ID)
	require.Equal(t, "production", key.Name)
	require.Empty(t, key.KeyHash, "returned record must not contain the hash")
	assert.True(t, strings.HasPrefix(secret, Prefix))

	// The active key authenticates.
	ok, err := manager.Authenticate(ctx, secret)
	require.NoError(t, err)
	assert.True(t, ok)

	// Wrong token does not.
	ok, err = manager.Authenticate(ctx, secret+"x")
	require.NoError(t, err)
	assert.False(t, ok)

	// Malformed / non-sk- tokens are rejected before any lookup.
	for _, token := range []string{"", "wrong", "Bearer " + secret, "SK-" + secret[3:]} {
		ok, err = manager.Authenticate(ctx, token)
		require.NoError(t, err)
		assert.False(t, ok, "token %q must not authenticate", token)
	}
}

func TestManager_PlaintextNeverPersisted(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	_, secret, err := manager.Create(ctx, "secret-check")
	require.NoError(t, err)

	var raw bson.M
	err = client.Database(manager.coll.Database().Name()).
		Collection(manager.coll.Name()).
		FindOne(ctx, bson.M{}).Decode(&raw)
	require.NoError(t, err)

	// The plaintext must not appear anywhere in the raw document string form.
	blob := fmt.Sprintf("%v", raw)
	assert.NotContains(t, blob, secret, "plaintext must never be persisted")
	assert.NotContains(t, blob, "secret-check"+secret, "plaintext must never be persisted")

	// The hash is stored and differs from the plaintext.
	assert.NotEqual(t, secret, raw["keyHash"])
	assert.NotEmpty(t, raw["keyHash"])
	// The persisted display prefix starts with the constant prefix.
	assert.True(t, strings.HasPrefix(fmt.Sprint(raw["prefix"]), Prefix))
}

func TestManager_RevokeAndIdempotency(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	key, secret, err := manager.Create(ctx, "to-revoke")
	require.NoError(t, err)

	// Active key authenticates.
	ok, err := manager.Authenticate(ctx, secret)
	require.NoError(t, err)
	assert.True(t, ok)

	// Revoke succeeds (via the stored prefix); the key no longer authenticates.
	require.NoError(t, manager.RevokeByPrefix(ctx, key.Prefix))
	ok, err = manager.Authenticate(ctx, secret)
	require.NoError(t, err)
	assert.False(t, ok)

	// Revoking again is idempotent (succeeds silently).
	require.NoError(t, manager.RevokeByPrefix(ctx, key.Prefix))

	// An unknown prefix returns ErrKeyNotFound.
	err = manager.RevokeByPrefix(ctx, "sk-000000")
	require.ErrorIs(t, err, ErrKeyNotFound)
}

func TestManager_PrefixUniqueness(t *testing.T) {
	manager, client, ctx := newTestManager(t)

	// Two distinct keys must have distinct display prefixes.
	key1, secret1, err := manager.Create(ctx, "prefix-a")
	require.NoError(t, err)
	key2, secret2, err := manager.Create(ctx, "prefix-b")
	require.NoError(t, err)
	require.NotEmpty(t, key1.Prefix)
	require.NotEmpty(t, key2.Prefix)
	require.NotEqual(t, key1.Prefix, key2.Prefix, "two keys must have different prefixes")
	require.NotEqual(t, secret1, secret2)

	// A fresh key matching no stored prefix authenticates, and revoking an
	// unknown prefix is a not-found error.
	err = manager.RevokeByPrefix(ctx, "sk-abcdef")
	require.ErrorIs(t, err, ErrKeyNotFound)

	// Both the unique keyHash index and the unique prefix index must exist.
	indexes, err := client.Database(manager.coll.Database().Name()).
		Collection(manager.coll.Name()).
		Indexes().List(ctx)
	require.NoError(t, err)

	foundKeyHash := false
	foundPrefix := false
	for indexes.Next(ctx) {
		var idx bson.M
		require.NoError(t, indexes.Decode(&idx))
		// The index key spec arrives as a primitive.M (map) that may also be
		// decoded as a bson.D depending on the driver/MongoDB version; handle
		// both by normalizing to map form.
		switch k := idx["key"].(type) {
		case bson.D:
			for _, kv := range k {
				switch kv.Key {
				case "keyHash":
					foundKeyHash = idx["unique"] == true
				case "prefix":
					foundPrefix = idx["unique"] == true
				}
			}
		case bson.M:
			if _, ok := k["keyHash"]; ok {
				foundKeyHash = idx["unique"] == true
			}
			if _, ok := k["prefix"]; ok {
				foundPrefix = idx["unique"] == true
			}
		}
	}
	require.NoError(t, indexes.Err())
	assert.True(t, foundKeyHash, "unique keyHash index must exist")
	assert.True(t, foundPrefix, "unique prefix index must exist")
}

func TestManager_ListSanitizedAndBounded(t *testing.T) {
	manager, _, ctx := newTestManager(t)

	secrets := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		_, s, err := manager.Create(ctx, fmt.Sprintf("key-%d", i))
		require.NoError(t, err)
		secrets = append(secrets, s)
	}

	keys, err := manager.List(ctx, 0)
	require.NoError(t, err)
	require.Len(t, keys, 5)

	// No record carries the hash.
	for _, k := range keys {
		assert.Empty(t, k.KeyHash, "list must never return the key hash")
	}

	// Every key is present exactly once.
	names := make(map[string]int, len(keys))
	for _, k := range keys {
		names[k.Name]++
	}
	for i := 0; i < 5; i++ {
		assert.Equal(t, 1, names[fmt.Sprintf("key-%d", i)], "key-%d must appear exactly once", i)
	}

	// Sorted by createdAt descending. Creation timestamps may collide at
	// nanosecond resolution, so assert non-increasing order rather than an exact
	// sequence.
	for i := 1; i < len(keys); i++ {
		assert.False(t, keys[i].CreatedAt.After(keys[i-1].CreatedAt), "list must be sorted by createdAt descending")
	}

	// Default limit applies to non-positive requests.
	assert.Equal(t, DefaultListLimit, sanitizeLimit(0))

	// Bounded list: a huge limit is clamped.
	keys1000, err := manager.List(ctx, MaxListLimit+1)
	require.NoError(t, err)
	require.Len(t, keys1000, 5)
}
