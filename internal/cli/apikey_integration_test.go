//go:build integration

package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"conduit/internal/mongo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localMongoURI is the URI for integration tests running on the host. The compose
// MongoDB runs as a single-node replica set (rs0) advertising its internal
// hostname, so directConnection=true is required to reach it from outside the
// compose network.
const localMongoURI = "mongodb://localhost:27017/?directConnection=true"

// cliKeyEnv connects to local MongoDB, sets the config env vars for the API key
// commands to use (including a unique database so tests never share state), and
// registers cleanup to drop the database and disconnect. This file is compiled
// only under the `integration` build tag, so there is no -short skip: the tests
// fail loudly if MongoDB is unavailable for a run that asked for integration
// suites. Redis is never dialed by the apikey commands, but config.Load still
// requires REDIS_URI to be present, so a valid-but-idle value is set.
func cliKeyEnv(t *testing.T) *mongo.Client {
	t.Helper()

	db := fmt.Sprintf("conduit_cli_test_%d", time.Now().UnixNano())
	t.Setenv("MONGODB_URI", localMongoURI)
	t.Setenv("MONGODB_DATABASE", db)
	// REDIS_URI is required by config.Load but never dialed by the apikey
	// command path (no redis.NewClient call), so it can be a non-dialed placeholder.
	t.Setenv("REDIS_URI", "redis://localhost:6379")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	client, err := mongo.NewClient(ctx, mongo.Config{URI: localMongoURI, Database: db}, discardLogger)
	if err != nil {
		cancel()
		t.Fatalf("MongoDB not available at %s: %v — start it with: docker compose -f compose.dev.yaml up -d mongo", localMongoURI, err)
	}
	t.Cleanup(func() {
		_ = client.Client.Database(db).Drop(context.Background())
		client.Close(context.Background())
		cancel()
	})
	return client
}

func TestAPIKeyCreateCommand(t *testing.T) {
	cliKeyEnv(t)

	root, buf := newRootCommandForTest(t)
	err := root.Run(context.Background(), []string{"conduit", "apikey", "create", "--name", "production"})
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "production")

	keyRow := findRow(t, out, "Key")
	keyLine := rowValue(t, keyRow)
	require.True(t, strings.HasPrefix(keyLine, "sk-"), "key must start with sk-, got %q", keyLine)
	secret := strings.TrimPrefix(keyLine, "sk-")
	require.GreaterOrEqual(t, len(secret), 40, "random portion must be substantial, got %q", keyLine)

	require.Contains(t, out, "Save this key. It will not be shown again.")

	idRow := rowValue(t, findRow(t, out, "ID"))
	require.NotEmpty(t, idRow)
}

// TestAPIKeyList_HidesSensitiveData verifies that listing never leaks the
// plaintext secret, its random portion, or the stored hash — regardless of
// status.
func TestAPIKeyList_HidesSensitiveData(t *testing.T) {
	cliKeyEnv(t)

	// Create a key via the CLI and capture its full secret.
	root, createBuf := newRootCommandForTest(t)
	err := root.Run(context.Background(), []string{"conduit", "apikey", "create", "--name", "list-me"})
	require.NoError(t, err)
	id := rowValue(t, findRow(t, createBuf.String(), "ID"))
	secret := rowValue(t, findRow(t, createBuf.String(), "Key"))
	require.NotEmpty(t, id)
	require.True(t, strings.HasPrefix(secret, "sk-"))
	suffix := strings.TrimPrefix(secret, "sk-")

	// List must show the key row but never the secret material.
	root2, listBuf := newRootCommandForTest(t)
	err = root2.Run(context.Background(), []string{"conduit", "apikey", "list"})
	require.NoError(t, err)
	out := listBuf.String()
	require.Contains(t, out, id)
	require.Contains(t, out, "list-me")
	require.Contains(t, out, "active")
	require.Contains(t, out, "CREATED")
	require.NotContains(t, out, secret)
	require.NotContains(t, out, suffix)
	require.NotContains(t, out, "keyHash")

	// After revoking, the status flips to revoked and the secret is still absent.
	root3, _ := newRootCommandForTest(t)
	err = root3.Run(context.Background(), []string{"conduit", "apikey", "revoke", "--id", id})
	require.NoError(t, err)

	root4, list2Buf := newRootCommandForTest(t)
	err = root4.Run(context.Background(), []string{"conduit", "apikey", "list"})
	require.NoError(t, err)
	out2 := list2Buf.String()
	require.Contains(t, out2, "revoked")
	require.NotContains(t, out2, secret)
	require.NotContains(t, out2, suffix)
	require.NotContains(t, out2, "keyHash")
}

func TestAPIKeyRevoke(t *testing.T) {
	cliKeyEnv(t)

	root, createBuf := newRootCommandForTest(t)
	err := root.Run(context.Background(), []string{"conduit", "apikey", "create", "--name", "revoke-me"})
	require.NoError(t, err)
	id := rowValue(t, findRow(t, createBuf.String(), "ID"))
	require.NotEmpty(t, id)

	root2, revBuf := newRootCommandForTest(t)
	err = root2.Run(context.Background(), []string{"conduit", "apikey", "revoke", "--id", id})
	require.NoError(t, err)
	require.Contains(t, revBuf.String(), "API key revoked.")

	// Revoking the same id again is idempotent: no error, same confirmation.
	root3, rev2Buf := newRootCommandForTest(t)
	err = root3.Run(context.Background(), []string{"conduit", "apikey", "revoke", "--id", id})
	require.NoError(t, err)
	require.Contains(t, rev2Buf.String(), "API key revoked.")

	// A well-formed-but-nonexistent hex id is ErrKeyNotFound, as is a malformed id.
	root4, _ := newRootCommandForTest(t)
	err = root4.Run(context.Background(), []string{"conduit", "apikey", "revoke", "--id", "0123456789abcdef01234567"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "api key not found")

	root5, _ := newRootCommandForTest(t)
	err = root5.Run(context.Background(), []string{"conduit", "apikey", "revoke", "--id", "not-an-id"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "api key not found")
}

// findRow returns the trimmed line whose first whitespace-separated field equals
// the given label (e.g. "ID"), or fails the test. tabwriter aligns columns with
// spaces, so the tab cell separator is not preserved in the buffer.
func findRow(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 && fields[0] == label {
			return trimmed
		}
	}
	t.Fatalf("row %q not found in output:\n%s", label, out)
	return ""
}

// rowValue returns everything after the label field of a tabwriter-aligned row.
func rowValue(t *testing.T, row string) string {
	t.Helper()
	fields := strings.Fields(row)
	require.GreaterOrEqual(t, len(fields), 2, "expected label + value in row %q", row)
	return strings.TrimSpace(strings.TrimPrefix(row, fields[0]))
}
