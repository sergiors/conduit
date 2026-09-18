package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"conduit/internal/config"
	"conduit/internal/processlock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempLockPath returns a lock path inside the test's temporary directory, so
// tests never touch /var/run and never require elevated permissions. It is
// named like the production lock to keep the intent obvious.
func tempLockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "conduit.lock")
}

// TestStartCommand_HoldsLockForRunLifetime: the lock is held for the entire
// runtime entrypoint and released only after it returns. The stub runner probes
// the same path from inside the run, proving the descriptor stays open; a
// post-run acquisition proves it was released on return.
func TestStartCommand_HoldsLockForRunLifetime(t *testing.T) {
	healthEnv(t)
	path := tempLockPath(t)

	var heldDuringRun bool
	run := func(_ context.Context, _ config.Config, _ *slog.Logger) error {
		probe, err := processlock.Acquire(path)
		heldDuringRun = errors.Is(err, processlock.ErrBusy)
		if probe != nil {
			probe.Close()
		}
		return nil
	}

	cmd := newCommandTree(discardLogger, io.Discard, path, run)
	require.NoError(t, cmd.Run(context.Background(), []string{"conduit", "start"}))
	assert.True(t, heldDuringRun, "lock must be held while the runtime runs")

	lock, err := processlock.Acquire(path)
	require.NoError(t, err, "lock must be released after run returns")
	require.NoError(t, lock.Close())
}

// TestStartCommand_FailsWhenLockHeld: `conduit start` surfaces a clear error
// naming the already-running `conduit start` instead of starting the runtime
// when the lock is already held, while still preserving ErrBusy for errors.Is.
func TestStartCommand_FailsWhenLockHeld(t *testing.T) {
	healthEnv(t)
	path := tempLockPath(t)

	held, err := processlock.Acquire(path)
	require.NoError(t, err)
	defer held.Close()

	ran := false
	run := func(_ context.Context, _ config.Config, _ *slog.Logger) error {
		ran = true
		return nil
	}

	cmd := newCommandTree(discardLogger, io.Discard, path, run)
	err = cmd.Run(context.Background(), []string{"conduit", "start"})
	require.Error(t, err)
	assert.ErrorIs(t, err, processlock.ErrBusy)
	assert.ErrorContains(t, err, "another conduit start is already running")
	assert.False(t, ran, "runtime must not start while the lock is held")
}

// TestUnrelatedCommandsDoNotAcquireLock: only `conduit start` takes the process
// lock. Running the other commands leaves the lock path untouched.
func TestUnrelatedCommandsDoNotAcquireLock(t *testing.T) {
	healthEnv(t)
	stubProbes(t, "healthy", "healthy")

	noRun := func(_ context.Context, _ config.Config, _ *slog.Logger) error {
		t.Fatal("runtime must not run for an unrelated command")
		return nil
	}

	lockPath := tempLockPath(t)

	health := newCommandTree(discardLogger, io.Discard, lockPath, noRun)
	require.NoError(t, health.Run(context.Background(), []string{"conduit", "health"}))

	apikey := newCommandTree(discardLogger, io.Discard, lockPath, noRun)
	require.NoError(t, apikey.Run(context.Background(), []string{"conduit", "apikey"}))

	_, err := os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err), "unrelated commands must not create the lock file")
}
