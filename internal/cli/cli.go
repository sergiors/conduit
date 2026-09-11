// Package cli implements the conduit command-line interface using
// github.com/urfave/cli/v3. It wires the executable-level commands (server,
// worker, health, apikey) to the underlying runtime packages.
package cli

import (
	"context"
	"log"

	"github.com/urfave/cli/v3"
)

// Run executes the conduit CLI with the given argument slice (typically
// os.Args[1:]). It is kept callable from cmd/main.go, which exits 1 and logs the
// returned error. urfave/cli prints usage errors itself; this only returns
// execution errors.
//
// The command tree is built fresh on each call so the logger is captured by the
// action closures for the runtime commands (server, worker, health, apikey),
// keeping their logging behavior identical to the pre-urfave CLI.
func Run(args []string, logger *log.Logger) error {
	root := &cli.Command{
		Name:  "conduit",
		Usage: "Conduit CDC platform",
		Commands: []*cli.Command{
			serverCommand(logger),
			workerCommand(logger),
			healthCommand(logger),
			apikeyCommand(logger),
		},
	}
	// urfave/cli interprets osArgs[0] as the root command name. main.go calls
	// Run(os.Args[1:], logger) (without "conduit"), so prepend the root name to
	// satisfy cli's dispatch.
	fullArgs := append([]string{root.Name}, args...)
	return root.Run(context.Background(), fullArgs)
}
