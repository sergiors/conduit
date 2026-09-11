package cli

import (
	"context"
	"log"

	"conduit/internal/api"
	"conduit/internal/config"

	"github.com/urfave/cli/v3"
)

// serverCommand returns the "conduit server" command that starts the API
// server. Behavior is unchanged from the pre-urfave runServer: it loads the
// full server config and blocks in api.Run.
func serverCommand(logger *log.Logger) *cli.Command {
	return &cli.Command{
		Name:  "server",
		Usage: "Start the API server",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg := config.Load(logger)
			return api.Run(cfg, logger)
		},
	}
}
