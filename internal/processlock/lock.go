package processlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrBusy reports that the lock is already held by another process. It is a
// sentinel so callers can distinguish the expected single-instance failure from
// a filesystem or syscall error. The package deliberately does not name any
// command in the message; callers that own user-facing wording wrap the
// returned error while preserving ErrBusy for errors.Is.
var ErrBusy = errors.New("lock is already held by another process")

// Lock is an acquired OS-level advisory lock backed by an open file descriptor.
// The descriptor is held for the lock's whole lifetime: the flock is tied to
// the open file description, so Close releases it and process exit releases it
// as part of the kernel tearing the descriptor down. A Lock is used by a single
// goroutine and is not safe for concurrent use.
type Lock struct {
	file *os.File
}

// Acquire ensures the parent directory of path exists, opens (or creates) path,
// and takes an exclusive, non-blocking flock on it. It returns a Lock whose
// descriptor must stay open for as long as the lock is meant to be held; Close
// releases it.
//
// A lock held by another process fails immediately with an error wrapping
// ErrBusy rather than blocking. An existing path with no live flock (for
// example a stale file left by a crashed process) is not considered held: the
// file is reopened and the lock acquired normally.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory for %s: %w", path, err)
	}

	// os.OpenFile adds O_CLOEXEC on Unix, so the descriptor is not inherited
	// by child processes.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", path, err)
	}

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		// The descriptor was opened for a lock that was never acquired; close
		// it here so a failed acquisition never leaks a file descriptor.
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w (lock %s)", ErrBusy, path)
		}
		return nil, fmt.Errorf("failed to lock %s: %w", path, err)
	}

	return &Lock{file: file}, nil
}

// Close releases the flock and closes the underlying file descriptor. The OS
// would release the lock on close anyway, but unlocking explicitly makes the
// release intentional and surfaces an unlock failure. Close is idempotent and
// nil-safe so a deferred call can run unconditionally.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}

	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
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
