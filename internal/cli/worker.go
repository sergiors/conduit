package cli

import (
	"context"
	"log"

	"conduit/internal/config"
	"conduit/internal/worker"

	"github.com/urfave/cli/v3"
)

// workerCommand returns the "conduit worker" command that starts the CDC
// worker. Behavior is unchanged from the pre-urfave runWorker: it loads the
// full config and blocks in worker.Run.
func workerCommand(logger *log.Logger) *cli.Command {
	return &cli.Command{
		Name:  "worker",
		Usage: "Start the CDC worker",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg := config.Load(logger)
			return worker.Run(cfg, logger)
		},
	}
}
