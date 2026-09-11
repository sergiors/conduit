package apikey

import (
	"context"
	"fmt"
	"io"
	"log"
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

var discardLogger = log.New(io.Discard, "", 0)

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

	// Revoke succeeds; the key no longer authenticates.
	require.NoError(t, manager.Revoke(ctx, key.ID))
	ok, err = manager.Authenticate(ctx, secret)
	require.NoError(t, err)
	assert.False(t, ok)

	// Revoking again is idempotent (succeeds silently).
	require.NoError(t, manager.Revoke(ctx, key.ID))

	// A nonexistent id returns ErrKeyNotFound.
	err = manager.Revoke(ctx, "000000000000000000000000")
	require.ErrorIs(t, err, ErrKeyNotFound)

	// A malformed id is treated as not found.
	err = manager.Revoke(ctx, "not-an-oid")
	require.ErrorIs(t, err, ErrKeyNotFound)
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
