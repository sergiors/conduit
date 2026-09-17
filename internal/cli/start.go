package cli

import (
	"context"
	"log/slog"

	"github.com/urfave/cli/v3"

	"conduit/internal/config"
	"conduit/internal/runtime"
)

// startCommand returns the "conduit start" command. It is a thin wrapper:
// the full config is loaded here (a config command concern), but the entire
// application lifecycle — shared infrastructure, the API server, the worker,
// graceful shutdown, and failure coordination — is owned by the composition
// root in internal/runtime, which receives the process root context owned by
// cmd/main.go.
func startCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Start the runtime (API server and worker)",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg := config.Load(logger)
			return runtime.Run(ctx, cfg, logger)
		},
	}
}
