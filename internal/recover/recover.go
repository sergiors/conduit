package recover

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Protect runs fn, recovering from a panic and reporting it through the given
// name at ERROR level with a full stack trace. It returns the recovered value
// and whether a panic occurred, so callers can decide to restart or abort.
//
// Protects are for goroutine boundaries (backstops): a recovered panic here
// prevents process death. Use ProtectErr for per-event/per-tick bodies that
// must let the surrounding loop continue.
func Protect(logger *slog.Logger, name string, fn func()) (recovered any, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			logPanic(logger, name, r)
			recovered, panicked = r, true
		}
	}()
	fn()
	return nil, false
}

// ProtectErr runs fn, recovering from a panic and reporting it through the
// given name at ERROR level with a full stack trace. It returns the recovered
// panic as an error and whether a panic occurred, so callers can log the
// failure and continue their loop.
//
// Normal errors returned by fn are passed through unchanged; recovery only
// engages on a panic.
func ProtectErr(logger *slog.Logger, name string, fn func() error) (err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			logPanic(logger, name, r)
			err, panicked = fmt.Errorf("panic in %s: %v", name, r), true
		}
	}()
	return fn(), false
}

// logPanic reports a recovered panic at ERROR with the component name, the
// panic value and a full stack trace. Logging at ERROR classifies a recovered
// panic as an operator-visible failure: it is the process-level backstop for a
// goroutine or loop that would otherwise crash the process.
func logPanic(logger *slog.Logger, name string, r any) {
	logger.Error(
		"Recovered panic",
		"component", name,
		"panic", r,
		"stack", string(debug.Stack()),
	)
}
