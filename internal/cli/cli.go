// Package cli implements the conduit command-line interface using
// github.com/urfave/cli/v3. It wires the executable-level commands (server,
// worker, health, apikey) to the underlying runtime packages.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
)

// New creates the Conduit CLI command tree. The writer is configured once on
// the root command; subcommands with a nil Writer inherit it at run time
// (urfave/cli setupSubcommand parent-writer inheritance), and actions write
// through cmd.Writer.
//
// An empty ExitErrHandler replaces urfave's default HandleExitCoder, which
// would os.Exit(1) inside the library for exit-coded errors (usage errors,
// unknown commands). Keeping errors returning from Run lets cmd/main.go own
// logging and the process exit code — a single place prints each failure.
func New(logger *slog.Logger, writer io.Writer) *cli.Command {
	return &cli.Command{
		Name:           "conduit",
		Usage:          "Conduit CDC platform",
		Writer:         writer,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{
			serverCommand(logger),
			workerCommand(logger),
			healthCommand(logger),
			apikeyCommand(logger),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if !cmd.Args().Present() {
				return cli.ShowAppHelp(cmd)
			}

			return fmt.Errorf(
				"conduit: unknown command: conduit %s\n\nRun 'conduit --help' for more information",
				cmd.Args().First(),
			)
		},
	}
}

// Run executes the Conduit CLI. args must be the complete argument slice
// urfave/cli expects (binary name first, e.g. os.Args). The returned error —
// a single failure — is logged and turned into the exit code by cmd/main.go;
// urfave/cli prints usage errors to Writer itself.
func Run(ctx context.Context, args []string, logger *slog.Logger) error {
	return New(logger, os.Stdout).Run(ctx, args)
}
