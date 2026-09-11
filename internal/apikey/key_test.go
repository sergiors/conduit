package apikey

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate_FormatEntropyUniqueness(t *testing.T) {
	now := time.Now()

	key1, secret1, err := Generate("production", now)
	require.NoError(t, err)
	_, secret2, err := Generate("production", now)
	require.NoError(t, err)

	// The secret has the "sk-" prefix and full random portion (256-bit entropy).
	assert.True(t, strings.HasPrefix(secret1, Prefix), "secret must start with %q", Prefix)
	assert.Equal(t, len(Prefix)+secretChars, len(secret1), "secret must be exactly prefix + random chars")
	assert.True(t, len(secret1)-len(Prefix) >= secretBytes/2, "random portion encodes >= 32 bytes of entropy")

	// Two consecutive calls never collide.
	assert.NotEqual(t, secret1, secret2, "secrets must be unique")
	assert.NotEqual(t, hashKey(secret1), hashKey(secret2), "hashes must be unique")

	// Record carries the display prefix, name, creation time, and no plaintext
	// nor hash (the hash is added only when persisting).
	assert.Equal(t, "production", key1.Name)
	assert.Equal(t, now, key1.CreatedAt)
	assert.Equal(t, PrefixOf(secret1), key1.Prefix)
	assert.Empty(t, key1.KeyHash, "generate must not set the hash on the returned record")
	for _, hash := range []string{hashKey(secret1), hashKey(secret2)} {
		assert.NotContains(t, secret1, hash)
		assert.NotContains(t, secret2, hash)
	}
}

func TestHashKey_DeterminismAndMismatch(t *testing.T) {
	_, s1, err := Generate("a", time.Now())
	require.NoError(t, err)
	_, s2, err := Generate("b", time.Now())
	require.NoError(t, err)

	// Hashing is deterministic for the same token.
	assert.Equal(t, hashKey(s1), hashKey(s1), "hashing must be deterministic")

	// Different tokens yield different hashes.
	assert.NotEqual(t, hashKey(s1), hashKey(s2))

	// The hash is never equal to the plaintext.
	assert.NotEqual(t, s1, hashKey(s1))
}

func TestPrefixOf_EdgeCases(t *testing.T) {
	_, secret, err := Generate("a", time.Now())
	require.NoError(t, err)

	// Normal key: prefix + first 6 random chars.
	assert.Equal(t, Prefix+secret[len(Prefix):len(Prefix)+prefixDisplayChars], PrefixOf(secret))
	assert.NotEqual(t, secret, PrefixOf(secret), "the display prefix reveals only a fragment")

	// Short input falls back to just the prefix.
	assert.Equal(t, Prefix, PrefixOf(""))
	assert.Equal(t, Prefix, PrefixOf("sk-short"))
	assert.Equal(t, Prefix, PrefixOf("sk-"+"12345"))

	// Input without the prefix that still has enough trailing chars.
	assert.Equal(t, Prefix+"abcdef", PrefixOf("abcdefghij"))
}

func TestValidToken_PureFunction(t *testing.T) {
	_, good, err := Generate("a", time.Now())
	require.NoError(t, err)

	assert.True(t, validToken(good), "a generated secret must be valid")

	invalid := map[string]bool{
		"":                 false,
		"Bearer " + good:   false,
		"sk-":              false,
		"SK-" + good[3:]:   false, // wrong case
		good[1:]:           false, // missing prefix char
		good + "x":         false, // too long
		good[:len(good)-1]: false, // too short
	}
	for token, want := range invalid {
		assert.Equal(t, want, validToken(token), "validToken(%q) should be %v", token, want)
	}
}

func TestSanitizeLimit(t *testing.T) {
	assert.Equal(t, DefaultListLimit, sanitizeLimit(0), "zero uses default")
	assert.Equal(t, DefaultListLimit, sanitizeLimit(-10), "negative uses default")
	assert.Equal(t, 50, sanitizeLimit(50), "in-range passes through")
	assert.Equal(t, MaxListLimit, sanitizeLimit(2000), "over cap is clamped")
	assert.Equal(t, MaxListLimit, sanitizeLimit(MaxListLimit), "exact cap is allowed")
}
