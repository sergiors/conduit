package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"conduit/internal/apikey"
	"conduit/internal/config"
	"conduit/internal/mongo"
)

// apikeyCommand returns the "conduit apikey" parent command with create, ls,
// and revoke subcommands. All subcommands load the same full config as every
// conduit command and then drive the persisted apikey.Manager over MongoDB;
// they never connect to Redis.
func apikeyCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:   "apikey",
		Usage:  "Manage API keys",
		Action: namespaceAction(),
		Commands: []*cli.Command{
			apikeyCreateCommand(logger),
			apikeyListCommand(logger),
			apikeyRevokeCommand(logger),
		},
	}
}

// apikeyCreateCommand returns the "conduit apikey create" command.
func apikeyCreateCommand(logger *slog.Logger) *cli.Command {
	var name string
	return &cli.Command{
		Name:      "create",
		Usage:     "Create an API key",
		ArgsUsage: " ",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "name",
				Aliases:     []string{"n"},
				Usage:       "API key name",
				Required:    true,
				Destination: &name,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runAPIKeyCreate(ctx, logger, cmd.Writer, name)
		},
	}
}

// runAPIKeyCreate opens MongoDB, creates a key with the given name, and prints
// its name and full plaintext secret — the only time the secret is shown — along
// with its short public identifier. The internal MongoDB id is never printed.
func runAPIKeyCreate(ctx context.Context, logger *slog.Logger, out io.Writer, name string) error {
	client, cancel, err := openMongo(ctx, logger)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	defer cancel()

	cfg := config.Load(logger)
	manager := apikey.NewManager(client.Client, cfg.MongoDBDatabase, logger)
	if err := manager.CreateIndex(ctx); err != nil {
		return fmt.Errorf("failed to create api key index: %w", err)
	}

	key, secret, err := manager.Create(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to create api key: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Name\t%s\n", key.Name)
	fmt.Fprintf(w, "Key\t%s\n", secret)
	w.Flush()

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Save this key. It will not be shown again.")
	return nil
}

// apikeyListCommand returns the "conduit apikey ls" command.
func apikeyListCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "ls",
		Usage:     "List API keys",
		ArgsUsage: " ",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runAPIKeyList(ctx, logger, cmd.Writer)
		},
	}
}

// runAPIKeyList opens MongoDB and prints a bounded table of keys (public
// prefix, name, created, status) with no MongoDB id, no secret, and no hash.
func runAPIKeyList(ctx context.Context, logger *slog.Logger, out io.Writer) error {
	client, cancel, err := openMongo(ctx, logger)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	defer cancel()

	cfg := config.Load(logger)
	manager := apikey.NewManager(client.Client, cfg.MongoDBDatabase, logger)

	keys, err := manager.List(ctx, 0)
	if err != nil {
		return fmt.Errorf("failed to list api keys: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tNAME\tCREATED\tSTATUS")
	for _, k := range keys {
		status := "active"
		if k.RevokedAt != nil {
			status = "revoked"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.Prefix, k.Name, k.CreatedAt.Format("2006-01-02 15:04"), status)
	}
	w.Flush()
	return nil
}

// apikeyRevokeCommand returns the "conduit apikey revoke <key>" command, where
// <key> is the short public identifier (e.g. sk-a8f3c2) shown by create and ls.
// Revoking an already-revoked key succeeds silently (idempotent). urfave/cli v3
// has no int-arg-count Required field on Command, so the number of positional
// arguments is validated manually in the action.
func apikeyRevokeCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:      "revoke",
		Usage:     "Revoke an API key",
		ArgsUsage: "<key>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if !args.Present() {
				return fmt.Errorf("usage: conduit apikey revoke <key>")
			}
			if args.Len() > 1 {
				return fmt.Errorf("usage: conduit apikey revoke <key>: unexpected extra argument %q", args.Get(1))
			}
			return runAPIKeyRevoke(ctx, logger, cmd.Writer, args.First())
		},
	}
}

// runAPIKeyRevoke opens MongoDB and revokes the key with the given public
// identifier (prefix), confirming the action to the operator.
func runAPIKeyRevoke(ctx context.Context, logger *slog.Logger, out io.Writer, prefix string) error {
	client, cancel, err := openMongo(ctx, logger)
	if err != nil {
		return err
	}
	defer client.Close(ctx)
	defer cancel()

	cfg := config.Load(logger)
	manager := apikey.NewManager(client.Client, cfg.MongoDBDatabase, logger)

	if err := manager.RevokeByPrefix(ctx, prefix); err != nil {
		return err
	}
	fmt.Fprintln(out, "API key revoked.")
	return nil
}

// openMongo connects to MongoDB and returns the wired client plus a cancel
// function that must be called to close the connection. It is shared by all the
// apikey subcommands so they bootstrap identically.
//
// Like every conduit command, openMongo loads the full config via config.Load,
// so REDIS_URI must be present in the environment even though the apikey
// commands never actually connect to Redis. Only the MongoDB fields of the
// resulting Config are consumed here.
//
// mongo.NewClient is given a discard logger so its readiness probes do not
// pollute the command's own concise output; config-loader warnings still print
// through the real logger.
func openMongo(ctx context.Context, logger *slog.Logger) (*mongo.Client, context.CancelFunc, error) {
	cfg := config.Load(logger)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	client, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	return client, cancel, nil
}
