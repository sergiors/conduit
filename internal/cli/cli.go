// Package cli implements the conduit command-line interface.
package cli

import (
	"errors"
	"fmt"
	"log"
	"os"
)

const usageText = `Usage:  conduit COMMAND

Commands:
  server    Start the API server
  worker    Start the CDC worker
  health    Check service health
`

// Run dispatches to the command named by args[0]. With no command (or an
// unknown one) it prints the usage to stderr and returns an error.
func Run(args []string, logger *log.Logger) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return errors.New("no command given")
	}

	switch args[0] {
	case "server":
		return runServer(logger)
	case "worker":
		return runWorker(logger)
	case "health":
		return runHealth(logger)
	default:
		fmt.Fprint(os.Stderr, usageText)
		return fmt.Errorf("unknown command: %q", args[0])
	}
}
