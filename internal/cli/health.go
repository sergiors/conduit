package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"

	"conduit/internal/config"
	"conduit/internal/mongo"
	"conduit/internal/redis"
)

// checkCtx returns a bounded context for a single dependency health probe.
func checkCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}

// mongoProbe / redisProbe are indirection seams around checkMongo/checkRedis so
// tests can stub them and assert the health table and joined errors without
// dialing real services.
var mongoProbe = checkMongo
var redisProbe = checkRedis

// healthCommand returns the "conduit health" command.
func healthCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:  "health",
		Usage: "Check service health",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runHealth(ctx, cmd.Writer, logger)
		},
	}
}

// checkMongo probes MongoDB connectivity by establishing a client and returns
// "healthy" on success, otherwise the error text. On success the client is
// closed before returning.
func checkMongo(ctx context.Context, cfg mongo.Config) string {
	probeLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := mongo.NewClient(ctx, cfg, probeLogger)
	if err != nil {
		return err.Error()
	}
	client.Close(ctx)
	return "healthy"
}

// checkRedis probes Redis connectivity by establishing a client and returns
// "healthy" on success, otherwise the error text. On success the client is
// closed before returning.
func checkRedis(ctx context.Context, cfg redis.Config) string {
	probeLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := redis.NewClient(ctx, cfg, probeLogger)
	if err != nil {
		return err.Error()
	}
	client.Close()
	return "healthy"
}

// runHealth checks MongoDB and Redis connectivity for the "health" command.
// Every failing dependency is still checked and reported so the operator sees
// the full picture, and an error is returned so the CLI exits non-zero on any
// failure.
func runHealth(ctx context.Context, out io.Writer, logger *slog.Logger) error {
	cfg := config.Load(logger)

	// Each check gets its own short timeout rather than sharing one context, so a
	// slow/failing MongoDB probe cannot exhaust Redis's window. Both checks stay
	// sequential and both always run.
	mongoStatus := "healthy"
	mongoCtx, mongoCancel := checkCtx(ctx)
	mongoStatus = mongoProbe(mongoCtx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	})
	mongoCancel()

	redisStatus := "healthy"
	redisCtx, redisCancel := checkCtx(ctx)
	redisStatus = redisProbe(redisCtx, redis.Config{
		URI:    cfg.RedisURI,
		Prefix: "cdc:",
	})
	redisCancel()

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "MongoDB\t%s\n", mongoStatus)
	fmt.Fprintf(w, "Redis\t%s\n", redisStatus)
	w.Flush()

	var errs []error
	if mongoStatus != "healthy" {
		errs = append(errs, fmt.Errorf("MongoDB %s", mongoStatus))
	}
	if redisStatus != "healthy" {
		errs = append(errs, fmt.Errorf("Redis %s", redisStatus))
	}
	return errors.Join(errs...)
}
