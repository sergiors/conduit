package processlock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempLockPath returns a lock path inside the test's temporary directory,
// nested one level so the directory-creation behavior is exercised, and never
// touches a real system path.
func tempLockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "conduit.lock")
}

// TestAcquire_FirstAcquisition: an unlocked path is acquired, the parent
// directory is created, and the lock can be released cleanly.
func TestAcquire_FirstAcquisition(t *testing.T) {
	path := tempLockPath(t)

	lock, err := Acquire(path)
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.FileExists(t, path)

	require.NoError(t, lock.Close())
}

// TestAcquire_BusyWhileHeld: a second acquisition of a path whose lock is
// already held must fail immediately and report ErrBusy, rather than block or
// succeed. The error must both satisfy errors.Is and name the path.
func TestAcquire_BusyWhileHeld(t *testing.T) {
	path := tempLockPath(t)

	first, err := Acquire(path)
	require.NoError(t, err)
	defer first.Close()

	second, err := Acquire(path)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBusy)
	assert.ErrorContains(t, err, path)
	assert.Nil(t, second)
}

// TestAcquire_ReacquireAfterRelease: once the held lock is closed, the same
// path can be locked again.
func TestAcquire_ReacquireAfterRelease(t *testing.T) {
	path := tempLockPath(t)

	first, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := Acquire(path)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NoError(t, second.Close())
}

// TestAcquire_StaleFileNotHeld: an existing lock file with no live flock (e.g.
// left by a crashed process) is not treated as held; the lock is acquired
// normally and the stale contents are preserved.
func TestAcquire_StaleFileNotHeld(t *testing.T) {
	path := tempLockPath(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("stale"), 0o644))

	lock, err := Acquire(path)
	require.NoError(t, err)
	require.NotNil(t, lock)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "stale", string(data), "acquiring must not truncate the lock file")

	require.NoError(t, lock.Close())
}

// TestAcquire_CreatesParentDirectories: Acquire creates the full parent chain
// of the lock path when it does not yet exist.
func TestAcquire_CreatesParentDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	path := filepath.Join(dir, "conduit.lock")

	lock, err := Acquire(path)
	require.NoError(t, err)
	defer lock.Close()

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "parent path must be a directory")
}

// TestAcquire_OpenFailureIsNotBusy: an unopenable path (here, a directory) must
// surface a filesystem error, not ErrBusy, so callers can distinguish the
// single-instance case from a real failure.
func TestAcquire_OpenFailureIsNotBusy(t *testing.T) {
	path := t.TempDir() // a directory cannot be opened as a lock file with O_RDWR

	lock, err := Acquire(path)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrBusy)
	assert.Nil(t, lock)
}

// TestClose_IdempotentAndNilSafe: a deferred Close can run unconditionally; a
// second Close and a Close on a nil *Lock are both no-ops that return nil.
func TestClose_IdempotentAndNilSafe(t *testing.T) {
	path := tempLockPath(t)

	lock, err := Acquire(path)
	require.NoError(t, err)

	require.NoError(t, lock.Close())
	assert.NoError(t, lock.Close(), "second Close must be a no-op")

	var nilLock *Lock
	assert.NoError(t, nilLock.Close(), "nil receiver Close must be a no-op")
}

// TestClose_ReleasesForAnotherHolder: closing releases the OS lock so a fresh
// acquisition — representing another process — succeeds, and a stale file that
// remains on disk afterwards is not mistaken for a held lock.
func TestClose_ReleasesForAnotherHolder(t *testing.T) {
	path := tempLockPath(t)

	first, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	// The file remains on disk; only the kernel lock was released.
	require.FileExists(t, path)

	second, err := Acquire(path)
	require.NoError(t, err)
	defer second.Close()

	third, err := Acquire(path)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBusy)
	assert.Nil(t, third)
}

// TestAcquire_NoDirectoryComponent: a bare filename (filepath.Dir == ".")
// must not fail directory creation.
func TestAcquire_NoDirectoryComponent(t *testing.T) {
	t.Chdir(t.TempDir())

	lock, err := Acquire("conduit.lock")
	require.NoError(t, err)
	require.FileExists(t, "conduit.lock")
	require.NoError(t, lock.Close())
}
