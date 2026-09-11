// Command conduit is the single entrypoint for the conduit CLI. It owns
// process-level concerns (root context, signals, logger, os.Args, exit code);
// runtime logic lives in internal/cli and the underlying packages.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"conduit/internal/cli"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := cli.Run(ctx, os.Args, logger); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
