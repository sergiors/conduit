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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempLockPath returns a lock path inside the test's temporary directory, so
// tests never touch /run and never require elevated permissions.
func tempLockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "start.lock")
}

// TestAcquireProcessLock_FirstAcquisition: an unlocked path is acquired, the
// parent directory is created, and the lock can be released cleanly.
func TestAcquireProcessLock_FirstAcquisition(t *testing.T) {
	path := tempLockPath(t)

	lock, err := acquireProcessLock(path)
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.FileExists(t, path)

	require.NoError(t, lock.Close())
}

// TestAcquireProcessLock_SecondFailureWhileHeld: a second acquisition of a path
// whose lock is already held must fail immediately and report the sentinel,
// rather than block or succeed. The message must name `conduit start` so the
// operator knows exactly which command is already running.
func TestAcquireProcessLock_SecondFailureWhileHeld(t *testing.T) {
	path := tempLockPath(t)

	first, err := acquireProcessLock(path)
	require.NoError(t, err)
	defer first.Close()

	second, err := acquireProcessLock(path)
	require.Error(t, err)
	assert.ErrorIs(t, err, errLockHeld)
	assert.ErrorContains(t, err, "another conduit start is already running")
	assert.Nil(t, second)
}

// TestAcquireProcessLock_ReacquireAfterRelease: once the held lock is closed,
// the same path can be locked again.
func TestAcquireProcessLock_ReacquireAfterRelease(t *testing.T) {
	path := tempLockPath(t)

	first, err := acquireProcessLock(path)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := acquireProcessLock(path)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NoError(t, second.Close())
}

// TestAcquireProcessLock_StaleFileNotHeld: an existing lock file with no live
// flock (e.g. left by a crashed process) is not treated as held; the lock is
// acquired normally.
func TestAcquireProcessLock_StaleFileNotHeld(t *testing.T) {
	path := tempLockPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("stale"), 0o644))

	lock, err := acquireProcessLock(path)
	require.NoError(t, err)
	require.NotNil(t, lock)
	require.NoError(t, lock.Close())
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
		probe, err := acquireProcessLock(path)
		heldDuringRun = errors.Is(err, errLockHeld)
		if probe != nil {
			probe.Close()
		}
		return nil
	}

	cmd := newCommandTree(discardLogger, io.Discard, path, run)
	require.NoError(t, cmd.Run(context.Background(), []string{"conduit", "start"}))
	assert.True(t, heldDuringRun, "lock must be held while the runtime runs")

	lock, err := acquireProcessLock(path)
	require.NoError(t, err, "lock must be released after run returns")
	require.NoError(t, lock.Close())
}

// TestStartCommand_FailsWhenLockHeld: `conduit start` surfaces a clear error
// naming the already-running `conduit start` instead of starting the runtime
// when the lock is already held.
func TestStartCommand_FailsWhenLockHeld(t *testing.T) {
	healthEnv(t)
	path := tempLockPath(t)

	held, err := acquireProcessLock(path)
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
	assert.ErrorIs(t, err, errLockHeld)
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
