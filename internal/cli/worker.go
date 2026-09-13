package cli

import (
	"context"
	"log/slog"

	"github.com/urfave/cli/v3"

	"conduit/internal/config"
	"conduit/internal/worker"
)

// workerRun dispatches to the worker runtime. It is an indirection seam so tests
// can override it to assert the worker command dispatches with the loaded config
// and logger instead of actually starting (and blocking on) the worker.
var workerRun = worker.Run

// workerCommand returns the "conduit worker" command that loads the full config
// and blocks in the worker runtime.
func workerCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:  "worker",
		Usage: "Start the CDC worker",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg := config.Load(logger)
			return workerRun(cfg, logger)
		},
	}
}
