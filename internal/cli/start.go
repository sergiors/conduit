package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/urfave/cli/v3"

	"conduit/internal/config"
	"conduit/internal/processlock"
)

// startLockPath is the on-disk path of the process lock taken by
// `conduit start`. It lives under /var/run, which is cleared on boot, so a
// stale file left behind by an unclean shutdown never survives a reboot; and
// the lock itself is an OS advisory flock that is released automatically when
// the file descriptor is closed or the process dies. The file's existence is
// therefore never the source of truth — the live flock is.
const startLockPath = "/var/run/conduit/conduit.lock"

// startRunner is the runtime entrypoint `conduit start` delegates to. The
// composition-root Run signature is injected as a value (production passes
// runtime.Run) so the command's lock behavior can be exercised in tests without
// standing up MongoDB and Redis, and without a mutable package-level seam.
type startRunner func(ctx context.Context, cfg config.Config, logger *slog.Logger) error

// startCommand returns the "conduit start" command. It is a thin wrapper:
// the full config is loaded here (a config command concern), but the entire
// application lifecycle — shared infrastructure, the API server, the worker,
// graceful shutdown, and failure coordination — is owned by the composition
// root in internal/runtime, which receives the process root context owned by
// cmd/main.go.
//
// It is also the only command that takes an OS-level process lock (lockPath):
// start is the single-instance server command, so a second `conduit start`
// against the same host must fail fast instead of racing the first on the
// shared MongoDB/Redis state. The lock is acquired before the config is loaded
// and held — the open descriptor backing the flock stays open — for the whole
// run lifetime, then released when the action returns.
func startCommand(logger *slog.Logger, lockPath string, run startRunner) *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Start the runtime (API server and worker)",
		Action: func(ctx context.Context, _ *cli.Command) error {
			lock, err := processlock.Acquire(lockPath)
			if err != nil {
				if errors.Is(err, processlock.ErrBusy) {
					return fmt.Errorf("another conduit start is already running: %w", err)
				}
				return err
			}
			defer func() {
				if err := lock.Close(); err != nil {
					logger.Error("Failed to release process lock", "component", "runtime", "error", err)
				}
			}()

			cfg := config.Load(logger)
			return run(ctx, cfg, logger)
		},
	}
}
