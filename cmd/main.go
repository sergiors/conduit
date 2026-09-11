// Command conduit is the single entrypoint for the conduit CLI. It owns
// process-level concerns (root context, signals, logger, os.Args, exit code);
// runtime logic lives in internal/cli and the underlying packages.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"conduit/internal/cli"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)

	// The root context is cancelled on SIGINT/SIGTERM so in-flight commands
	// observe cancellation; the API server itself blocks in Router().Run (an
	// accepted, documented limitation) and terminates with the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args, logger); err != nil {
		logger.Println(err)
		os.Exit(1)
	}
}
