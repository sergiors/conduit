package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// startLockPath is the on-disk path of the process lock taken by
// `conduit start`. It lives under /run, which is cleared on boot, so a stale
// file left behind by an unclean shutdown never survives a reboot; and the
// lock itself is an OS advisory flock that is released automatically when the
// file descriptor is closed or the process dies. The file's existence is
// therefore never the source of truth — the live flock is.
const startLockPath = "/run/conduit/start.lock"

// errLockHeld reports that the start lock is already held by another running
// `conduit start`. It is a sentinel so callers can distinguish a busy lock
// (the expected single-instance failure) from a filesystem or syscall error.
// The message names the exact command the user must look for, since only
// `conduit start` takes the lock.
var errLockHeld = errors.New("another conduit start is already running")

// processLock is an acquired OS-level advisory lock backed by an open file
// descriptor. The descriptor is held for the lock's whole lifetime: the flock
// is tied to the open file description, so closing the lock releases it and
// process exit releases it as part of the kernel tearing the descriptor down.
// A processLock is used by a single goroutine and is not safe for concurrent
// use.
type processLock struct {
	file *os.File
}

// acquireProcessLock ensures the parent directory of path exists, opens (or
// creates) path, and takes an exclusive, non-blocking flock on it. It returns
// a processLock whose descriptor must stay open for as long as the lock is
// meant to be held; Close releases it.
//
// A lock held by another process fails immediately with an error wrapping
// errLockHeld rather than blocking. An existing path with no live flock (for
// example a stale file left by a crashed process) is not considered held: the
// file is reopened and the lock acquired normally.
func acquireProcessLock(path string) (*processLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory for %s: %w", path, err)
	}

	// os.OpenFile adds O_CLOEXEC on Unix, so the descriptor is not inherited
	// by child processes.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", path, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w (lock %s)", errLockHeld, path)
		}
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}

	return &processLock{file: file}, nil
}

// Close releases the flock and closes the underlying file descriptor. The OS
// would release the lock on close anyway, but unlocking explicitly makes the
// release intentional and surfaces an unlock failure. Close is idempotent and
// nil-safe so a deferred call can run unconditionally.
func (l *processLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}

	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil

	var errs []error
	if unlockErr != nil {
		errs = append(errs, fmt.Errorf("failed to release process lock: %w", unlockErr))
	}
	if closeErr != nil {
		errs = append(errs, fmt.Errorf("failed to close process lock file: %w", closeErr))
	}
	return errors.Join(errs...)
}
