// Command conduit is the single entrypoint for the conduit CLI. It dispatches
// to the CLI commands (server, worker, health); runtime logic lives in
// internal/cli and the underlying packages.
package main

import (
	"log"
	"os"

	"conduit/internal/cli"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	if err := cli.Run(os.Args[1:], logger); err != nil {
		logger.Println(err)
		os.Exit(1)
	}
}
