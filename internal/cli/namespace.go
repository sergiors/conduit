package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// namespaceAction returns the Action shared by the pure grouping (namespace)
// commands (apikey). These commands own no runtime work
// themselves; they only dispatch to their subcommands. A bare invocation
// (e.g. `conduit apikey`) means the operator asked for the command with no further
// token, so we show that command's own help rather than erroring — the list of
// subcommands IS the useful answer. An unknown first token (e.g. `conduit apikey
// asdsa`) is a typo or a stray argument, so we surface a Docker-style usage
// error naming the full command path and pointing at the exact help command.
//
// The error is built from the whole remaining arg slice (never empty here) so
// `conduit apikey foo bar` reports the full mistyped path, not just the first token.
// It returns via cli.Exit(msg, 2): a usage error, whose Error() is exactly msg
// so tests and cmd/main.go see the friendly text (the root ExitErrHandler is a
// silent no-op; only cmd/main.go prints and picks the exit code).
func namespaceAction() cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		if !cmd.Args().Present() {
			return cli.ShowSubcommandHelp(cmd)
		}
		full := cmd.FullName() + " " + strings.Join(cmd.Args().Slice(), " ")
		msg := fmt.Sprintf(
			"conduit: unknown command: %s\n\nUsage: %s\n\nRun '%s --help' for more information",
			full, cmd.FullName(), cmd.FullName(),
		)
		return cli.Exit(msg, 2)
	}
}
