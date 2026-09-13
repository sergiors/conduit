// Command conduit is the single entrypoint for the conduit CLI. It owns
// process-level concerns (root context, signals, logger, os.Args, exit code);
// runtime logic lives in internal/cli and the underlying packages.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"conduit/internal/cli"
	"conduit/internal/config"

	"github.com/lmittmann/tint"
)

func main() {
	// Bootstrap the process logger from LOG_LEVEL before any command runs, so
	// all process-level logging follows the configured level. LOG_LEVEL is
	// parsed here (fail-fast on an invalid value) and parsed again inside
	// config.Load as the commands dispatch; both agree, so the value is
	// validated exactly once at the executable boundary.
	level, err := config.ParseLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := slog.New(
		tint.NewTextHandler(os.Stdout, &tint.Options{
			Level:      level,
			TimeFormat: "2006-01-02 15:04:05",
		}),
	)

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
