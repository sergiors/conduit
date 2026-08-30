// Package recover provides panic-recovery helpers for long-running worker
// goroutines.
//
// A panic anywhere in a worker goroutine would otherwise crash the whole
// process. This package bounds recovery to goroutine and iteration boundaries:
// a backstop at the goroutine boundary prevents process death, while a
// per-event/per-tick isolation so a single bad item cannot kill its loop.
//
// It only ever engages on a panic. Normal errors flow through the callers'
// own logic untouched.
package recover
