package cli

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"conduit/internal/apikey"
	"conduit/internal/config"
	"conduit/internal/mongo"
)

// apikeyCommand returns the "conduit apikey" parent command with create, list,
// and revoke subcommands. All subcommands load the same full config as every
// conduit command and then drive the persisted apikey.Manager over MongoDB;
// they never connect to Redis.
func apikeyCommand(logger *log.Logger) *cli.Command {
	return &cli.Command{
		Name:  "apikey",
		Usage: "Manage API keys",
		Commands: []*cli.Command{
			apikeyCreateCommand(logger),
			apikeyListCommand(logger),
			apikeyRevokeCommand(logger),
		},
	}
}

// apikeyCreateCommand returns the "conduit apikey create" command. It creates a
// persisted key and prints its plaintext secret exactly once.
func apikeyCreateCommand(logger *log.Logger) *cli.Command {
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
			client, cancel, err := openMongo(logger)
			if err != nil {
				return err
			}
			defer client.Close(context.Background())
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

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ID\t%s\n", key.ID)
			fmt.Fprintf(w, "Name\t%s\n", key.Name)
			fmt.Fprintf(w, "Key\t%s\n", secret)
			w.Flush()

			fmt.Println()
			fmt.Println("Save this key. It will not be shown again.")
			return nil
		},
	}
}

// apikeyListCommand returns the "conduit apikey list" command. It prints a
// bounded table of keys (id, name, created, status) with no secret or hash.
func apikeyListCommand(logger *log.Logger) *cli.Command {
	return &cli.Command{
		Name:      "list",
		Usage:     "List API keys",
		ArgsUsage: " ",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client, cancel, err := openMongo(logger)
			if err != nil {
				return err
			}
			defer client.Close(context.Background())
			defer cancel()

			cfg := config.Load(logger)
			manager := apikey.NewManager(client.Client, cfg.MongoDBDatabase, logger)

			keys, err := manager.List(ctx, 0)
			if err != nil {
				return fmt.Errorf("failed to list api keys: %w", err)
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tCREATED\tSTATUS")
			for _, k := range keys {
				status := "active"
				if k.RevokedAt != nil {
					status = "revoked"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", k.ID, k.Name, k.CreatedAt.Format("2006-01-02 15:04"), status)
			}
			w.Flush()
			return nil
		},
	}
}

// apikeyRevokeCommand returns the "conduit apikey revoke" command. It revokes a
// key by id; a nonexistent id is an error, while revoking an already-revoked
// key succeeds silently (idempotent).
func apikeyRevokeCommand(logger *log.Logger) *cli.Command {
	var id string
	return &cli.Command{
		Name:      "revoke",
		Usage:     "Revoke an API key",
		ArgsUsage: " ",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "id",
				Usage:       "API key ID",
				Required:    true,
				Destination: &id,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client, cancel, err := openMongo(logger)
			if err != nil {
				return err
			}
			defer client.Close(context.Background())
			defer cancel()

			cfg := config.Load(logger)
			manager := apikey.NewManager(client.Client, cfg.MongoDBDatabase, logger)

			if err := manager.Revoke(ctx, id); err != nil {
				return err
			}
			fmt.Println("API key revoked.")
			return nil
		},
	}
}

// openMongo connects to MongoDB using the MONGODB_URI / MONGODB_DATABASE env
// vars and returns the wired client plus a cancel function that must be called
// to close the connection. It is shared by all the apikey subcommands so they
// bootstrap identically without duplicating connection code.
//
// Like every conduit command, openMongo loads the full config via config.Load,
// so REDIS_URI must be present in the environment even though the apikey
// commands never actually connect to Redis (no redis.NewClient in their path).
// Only the MongoDB fields of the resulting Config are consumed here.
//
// mongo.NewClient is given a discard logger so its readiness probes
// ("MongoDB node is writable PRIMARY" / "MongoDB client ready") do not pollute
// the command's own concise output, mirroring the health command's probe
// suppression. Config-loader warnings still print through the real logger.
func openMongo(logger *log.Logger) (*mongo.Client, context.CancelFunc, error) {
	cfg := config.Load(logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	client, err := mongo.NewClient(ctx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, log.New(io.Discard, "", 0))
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	return client, cancel, nil
}
