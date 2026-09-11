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

// TestAPIKeyRequiredFlags_MissingID mirrors MissingName for the revoke command.
func TestAPIKeyRequiredFlags_MissingID(t *testing.T) {
	_, err := runRoot(t, []string{"apikey", "revoke"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id")
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
