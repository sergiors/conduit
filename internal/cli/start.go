package cli

import (
	"context"
	"log/slog"
	"sync"

	"github.com/urfave/cli/v3"

	"conduit/internal/api"
	"conduit/internal/config"
	"conduit/internal/worker"
)

// apiRun dispatches to the API runtime. It is an indirection seam so tests can
// override it to assert the start command dispatches with the loaded config and
// logger without actually starting (and blocking on) the API server.
var apiRun = api.Run

// workerRun dispatches to the worker runtime. It is an indirection seam so tests
// can override it to assert the start command dispatches with the loaded config
// and logger without actually starting (and blocking on) the worker.
var workerRun = worker.Run

// startCommand returns the "conduit start" command. It loads the full config
// once and runs both critical runtime components — the API server and the CDC
// worker — concurrently off a single context derived from the process root
// context (owned by cmd/main.go). Each component shuts down gracefully when the
// context is cancelled; if either fails to start or run, its error cancels the
// other's runtime, the group waits for both to wind down, and the first error
// propagates to the exit boundary.
func startCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Start the runtime (API server and worker)",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg := config.Load(logger)

			// runCtx is cancelled when the process root context is cancelled
			// (SIGINT/SIGTERM) or when the first component fails, so both
			// components shut down gracefully before it is awaited.
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			errs := make(chan error, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				errs <- apiRun(runCtx, cfg, logger)
			}()
			go func() {
				defer wg.Done()
				errs <- workerRun(runCtx, cfg, logger)
			}()

			// Propagate the first component failure immediately so the sibling
			// starts shutting down, but still wait for both (drain errs) before
			// returning. On clean shutdown both send nil.
			var firstErr error
			for i := 0; i < 2; i++ {
				if err := <-errs; err != nil && firstErr == nil {
					firstErr = err
					cancel()
				}
			}
			wg.Wait()
			return firstErr
		},
	}
}
