package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIKeyRequiredFlags_MissingName: a missing required flag is rejected by
// urfave/cli before the action runs, so config.Load is never reached and no env
// vars need to be set. Only the flag name is asserted, not the exact phrasing.
func TestAPIKeyRequiredFlags_MissingName(t *testing.T) {
	_, err := runRoot(t, []string{"apikey", "create"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

// TestAPIKeyRevokeMissingArg: revoking with no positional argument is rejected
// with a usage-style error before config.Load runs (so no env vars are needed).
// The error must not be an empty-flag failure and must mention the expected usage.
func TestAPIKeyRevokeMissingArg(t *testing.T) {
	_, err := runRoot(t, []string{"apikey", "revoke"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage")
	assert.Contains(t, err.Error(), "revoke <key>")
}

// TestAPIKeyRevokeHelpShowsPositionalSyntax: the revoke help (invoked without an
// action) advertises the positional <key> syntax instead of an --id flag.
func TestAPIKeyRevokeHelpShowsPositionalSyntax(t *testing.T) {
	out, err := runRoot(t, []string{"apikey", "revoke", "--help"})
	require.NoError(t, err)
	assert.Contains(t, string(out), "<key>")
	assert.NotContains(t, string(out), "--id")
}

// TestOpenMongoConnectFailure pins down openMongo's error contract: it fails at
// URI parse time (no dialing) and returns nil client and nil cancel.
func TestOpenMongoConnectFailure(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongo://bad^uri") // syntactically invalid -> parse failure
	t.Setenv("MONGODB_DATABASE", "conduit_test")
	t.Setenv("REDIS_URI", "redis://localhost:6379")

	client, cancel, err := openMongo(context.Background(), discardLogger)
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to connect to MongoDB")
	assert.Nil(t, client)
	assert.Nil(t, cancel)
}
