// Package processlock provides an OS-level advisory process lock backed by a
// file descriptor, used to enforce single-instance commands such as
// `conduit start`.
//
// A lock is an exclusive, non-blocking flock(2) on a caller-supplied path.
// The locking primitive is the live flock, never the existence of the file: a
// stale file left behind by a crashed process carries no kernel lock, so it is
// reopened and acquired normally. The descriptor is held open for the lock's
// whole lifetime and is released automatically by the kernel when the process
// exits, which makes the lock safe against unclean shutdowns.
//
// Acquire creates the parent directory, opens (or creates) the path, and
// returns a *Lock. A second acquisition of a path whose lock is already held
// fails immediately with an error wrapping [ErrBusy] rather than blocking.
// Close releases the flock and closes the descriptor; it is idempotent and
// nil-safe so a deferred call can run unconditionally.
//
// The package is intentionally generic: it knows nothing about commands or
// their user-facing wording. Callers own any command-specific error context by
// wrapping the returned error while preserving [ErrBusy] for errors.Is.
package processlock
