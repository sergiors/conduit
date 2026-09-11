package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Prefix is the leading identifier of every Conduit API key. It is used to
// classify tokens (authenticate rejects anything that does not start with it)
// and to build the display-only Prefix field.
const Prefix = "sk-"

// secretBytes is the number of crypto/rand bytes that make up the opaque
// portion of every API key, giving 256 bits of entropy (32 bytes).
const secretBytes = 32

// prefixDisplayChars is the number of random characters from the key's secret
// portion included in PrefixOf for human display. It identifies a key without
// ever being sufficient to forge it.
const prefixDisplayChars = 6

// secretChars is the number of base64url characters produced by encoding
// secretBytes bytes without padding: ceil(32/3)*4 = 44 minus the trailing
// padding, which RawURLEncoding omits. 32 bytes -> 42.67 -> 43 chars.
const secretChars = 43

// Key is a persisted API key. Only the SHA-256 hash of the key material is
// stored (KeyHash); the plaintext "sk-..." secret is returned exactly once at
// creation and cannot be recovered. RevokedAt, when non-nil, marks the key as
// permanently revoked.
//
// KeyHash has a json omitempty tag (and is always stripped before records leave
// the package via List) so the hash never appears in client-facing responses.
type Key struct {
	ID        string     `bson:"_id,omitempty" json:"_id,omitempty"`
	Name      string     `bson:"name" json:"name"`
	Prefix    string     `bson:"prefix" json:"prefix"`
	KeyHash   string     `bson:"keyHash" json:"keyHash,omitempty"`
	CreatedAt time.Time  `bson:"createdAt" json:"createdAt"`
	RevokedAt *time.Time `bson:"revokedAt,omitempty" json:"revokedAt,omitempty"`
}

// withHash returns a copy of k with KeyHash set to hash. It is used by the
// manager to persist a key while keeping the plaintext secret out of the
// returned record.
func (k Key) withHash(hash string) Key {
	k.KeyHash = hash
	return k
}

// Generate creates a new API key record and its plaintext secret. It returns
// the record WITHOUT any plaintext material and WITHOUT the hash (KeyHash is
// left empty); the manager computes and persists the hash separately so the
// hash can never escape the package. The full "sk-..." secret must be shown to
// the operator exactly once.
func Generate(name string, now time.Time) (Key, string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return Key{}, "", fmt.Errorf("generate api key secret: %w", err)
	}

	secret := Prefix + base64.RawURLEncoding.EncodeToString(buf)

	return Key{
		Name:      name,
		Prefix:    PrefixOf(secret),
		CreatedAt: now,
	}, secret, nil
}

// PrefixOf returns a short, display-only identifier for a key: the constant
// prefix plus the first six characters of the random portion (e.g. "sk-abc123").
// It is used only for human-readable display; it does not reveal a forged key.
// Short or malformed input falls back to just the constant prefix.
func PrefixOf(key string) string {
	rest := strings.TrimPrefix(key, Prefix)
	if len(rest) < prefixDisplayChars {
		return Prefix
	}
	return Prefix + rest[:prefixDisplayChars]
}

// hashKey hashes the full "sk-..." key string with SHA-256 and returns its hex
// encoding. The complete token (including the prefix) is hashed, nothing is
// stripped, so two tokens must differ by at least one byte to differ in hash.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// validToken reports whether token is structurally a Conduit API key: it must
// carry the "sk-" prefix and the full random portion, and be exactly as long as
// Generate produces. This is a pure function so the structural rules are
// unit-testable without a MongoDB connection. Unknown-but-well-formed tokens
// simply fail to match a stored hash rather than being rejected here, so
// checking shape does not leak anything about which keys exist.
func validToken(token string) bool {
	return strings.HasPrefix(token, Prefix) && len(token) == len(Prefix)+secretChars
}
