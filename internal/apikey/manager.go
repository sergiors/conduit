package apikey

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Sentinel errors returned by Manager. Callers MUST identify them with
// errors.Is so behavior does not depend on fragile string matching.
var (
	// ErrValidation is returned when a create request is malformed (e.g. an
	// empty name).
	ErrValidation = errors.New("validation failed")
	// ErrKeyNotFound is returned when a revoke targets an id that does not
	// exist. Revoking an already-revoked key is NOT an error (it is idempotent
	// and succeeds silently).
	ErrKeyNotFound = errors.New("api key not found")
)

// DefaultListLimit is the cap applied when a List call asks for no or a
// non-positive limit.
const DefaultListLimit = 100

// MaxListLimit is the hard upper bound for a single List page.
const MaxListLimit = 1000

// maxCreateAttempts is how many times Create will regenerate and re-insert when
// a SHA-256 hash collision is reported by the unique keyHash index. Collisions
// are astronomically unlikely; a couple of retries make Create robust to them.
const maxCreateAttempts = 3

// Manager owns the lifecycle of persisted API keys stored in the
// config.apiKeys MongoDB collection. It never logs or otherwise surfaces key
// material or hashes.
type Manager struct {
	coll   *mongo.Collection
	logger *log.Logger
}

// NewManager creates a Manager for the config.apiKeys collection in the given
// database.
func NewManager(client *mongo.Client, database string, logger *log.Logger) *Manager {
	return &Manager{
		coll:   client.Database(database).Collection("config.apiKeys"),
		logger: logger,
	}
}

// CreateIndex creates the unique index on keyHash that backs both the
// at-most-once storage of any secret and the fast Authenticate lookup.
func (m *Manager) CreateIndex(ctx context.Context) error {
	index := mongo.IndexModel{
		Keys:    bson.D{{Key: "keyHash", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	_, err := m.coll.Indexes().CreateOne(ctx, index)
	return err
}

// Create validates name, generates a fresh key, and persists only its hash. It
// returns the record (without plaintext) plus the full "sk-..." secret that
// must be shown to the caller exactly once.
func (m *Manager) Create(ctx context.Context, name string) (*Key, string, error) {
	if name == "" {
		return nil, "", ErrValidation
	}

	var key Key
	var secret string
	for attempt := 0; ; attempt++ {
		k, s, err := Generate(name, time.Now())
		if err != nil {
			return nil, "", err
		}
		if err := m.insert(ctx, &k, s); err != nil {
			// A hash collision is the only expected duplicate: same keyHash with
			// a fresh random secret means the index caught a collision. Retry with
			// a freshly generated secret rather than failing the operator.
			if mongo.IsDuplicateKeyError(err) && attempt < maxCreateAttempts-1 {
				continue
			}
			return nil, "", err
		}
		key, secret = k, s
		break
	}

	return &key, secret, nil
}

// insert persists a single key record and its hash, wiring the ObjectID into
// the record's ID field.
func (m *Manager) insert(ctx context.Context, key *Key, secret string) error {
	doc := key.withHash(hashKey(secret))
	res, err := m.coll.InsertOne(ctx, doc)
	if err != nil {
		return err
	}
	key.ID = res.InsertedID.(primitive.ObjectID).Hex()
	return nil
}

// Authenticate reports whether token is a currently-active API key. It returns
// (false, nil) for any structurally-invalid, unknown, or revoked token, and
// (true, nil) for a valid active key. The only non-nil error is an
// infrastructure failure (e.g. MongoDB unreachable). Tokens that are
// structurally wrong are rejected before any database lookup. Key material and
// hashes are never logged.
func (m *Manager) Authenticate(ctx context.Context, token string) (bool, error) {
	if !validToken(token) {
		return false, nil
	}

	var key Key
	err := m.coll.FindOne(ctx, bson.M{"keyHash": hashKey(token)}).Decode(&key)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("authenticate api key: %w", err)
	}
	if key.RevokedAt != nil {
		return false, nil
	}
	return true, nil
}

// List returns up to limit active-and-revoked keys sorted by createdAt
// descending. The returned records NEVER carry the keyHash. A non-positive or
// missing limit selects DefaultListLimit; larger limits are capped at
// MaxListLimit. keyHash is omitted from the projection so it cannot leak to the
// client even if a future caller forgets to sanitize.
func (m *Manager) List(ctx context.Context, limit int) ([]Key, error) {
	limit = sanitizeLimit(limit)

	findOpts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(int64(limit)).
		SetProjection(bson.M{"keyHash": 0})

	cursor, err := m.coll.Find(ctx, bson.M{}, findOpts)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer cursor.Close(ctx)

	var keys []Key
	if err := cursor.All(ctx, &keys); err != nil {
		return nil, fmt.Errorf("decode api keys: %w", err)
	}
	return keys, nil
}

// Revoke permanently revokes the key with the given id. Revoking an
// already-revoked key succeeds silently (idempotent); revoking a nonexistent id
// returns ErrKeyNotFound. The revocation is a race-tolerant conditional update
// that sets revokedAt only when it is currently absent, so two concurrent
// revocations both succeed.
func (m *Manager) Revoke(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return ErrKeyNotFound
	}

	now := time.Now()
	res, err := m.coll.UpdateOne(
		ctx,
		bson.M{"_id": oid, "revokedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"revokedAt": now}},
	)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if res.MatchedCount == 0 {
		// Either the key does not exist, or it is already revoked. Distinguish so
		// a nonexistent id is surfaced as ErrKeyNotFound while an already-revoked
		// key (revoke being idempotent) succeeds.
		var existing Key
		err := m.coll.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&existing)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("revoke api key: %w", err)
		}
	}
	return nil
}

// sanitizeLimit clamps a requested page limit into [1, MaxListLimit]. A
// non-positive limit selects DefaultListLimit. It is a pure function so it is
// unit-testable without MongoDB.
func sanitizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}
