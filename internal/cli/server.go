package cli

import (
	"context"
	"log"

	"conduit/internal/api"
	"conduit/internal/config"

	"github.com/urfave/cli/v3"
)

// apiRun dispatches to the API runtime. It is an indirection seam so tests can
// override it to assert the server command dispatches with the loaded config and
// logger instead of actually starting (and blocking on) the API server.
var apiRun = api.Run

// serverCommand returns the "conduit server" command that loads the full server
// config and blocks in the API runtime.
func serverCommand(logger *log.Logger) *cli.Command {
	return &cli.Command{
		Name:  "server",
		Usage: "Start the API server",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg := config.Load(logger)
			return apiRun(cfg, logger)
		},
	}
}
