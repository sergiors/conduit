package recover

import (
	"fmt"
	"log"
	"runtime/debug"
)

// Protect runs fn, recovering from a panic and reporting it through the given
// name in the log with a full stack trace. It returns the recovered value and
// whether a panic occurred, so callers can decide to restart or abort.
//
// Protects are for goroutine boundaries (backstops): a recovered panic here
// prevents process death. Use ProtectErr for per-event/per-tick bodies that
// must let the surrounding loop continue.
func Protect(name string, fn func()) (recovered any, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in %s: %v\n%s", name, r, debug.Stack())
			recovered, panicked = r, true
		}
	}()
	fn()
	return nil, false
}

// ProtectErr runs fn, recovering from a panic and reporting it through the
// given name in the log with a full stack trace. It returns the recovered
// panic as an error and whether a panic occurred, so callers can log the
// failure and continue their loop.
//
// Normal errors returned by fn are passed through unchanged; recovery only
// engages on a panic.
func ProtectErr(name string, fn func() error) (err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in %s: %v\n%s", name, r, debug.Stack())
			err, panicked = fmt.Errorf("panic in %s: %v", name, r), true
		}
	}()
	return fn(), false
}
