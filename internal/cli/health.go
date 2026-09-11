package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"github.com/sergiors/conduit/internal/config"
	"github.com/sergiors/conduit/internal/mongo"
	"github.com/sergiors/conduit/internal/redis"
)

// checkCtx returns a short-lived context for a single dependency health probe.
func checkCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// runHealth checks MongoDB and Redis connectivity for the "health" command.
// Every failing dependency is still checked and reported so the operator sees
// the full picture, and an error is returned so the CLI exits non-zero on any
// failure.
func runHealth(logger *log.Logger) error {
	cfg := config.LoadHealth(logger)

	// Probe noise (e.g. "MongoDB node is writable PRIMARY" / "MongoDB client
	// ready") is suppressed so the status table stays the only output beyond the
	// config-loader warnings. The injected logger is still passed to
	// config.LoadHealth so environment warnings print.
	probeLogger := log.New(io.Discard, "", 0)

	// Each check gets its own short timeout rather than sharing one context, so a
	// slow/failing MongoDB probe cannot exhaust Redis's window. Both checks stay
	// sequential and both always run.
	//
	// mongo.NewClient connects, pings, and waits for a writable PRIMARY before
	// returning, so a nil error here means MongoDB is healthy.
	mongoStatus := "healthy"
	mongoCtx, mongoCancel := checkCtx()
	mongoClient, err := mongo.NewClient(mongoCtx, mongo.Config{
		URI:      cfg.MongoDBURI,
		Database: cfg.MongoDBDatabase,
	}, probeLogger)
	if err != nil {
		mongoCancel()
		mongoStatus = err.Error()
	} else {
		mongoClient.Close(mongoCtx)
		mongoCancel()
	}

	// redis.NewClient pings on creation, so a nil error here means Redis healthy.
	redisStatus := "healthy"
	redisCtx, redisCancel := checkCtx()
	redisClient, err := redis.NewClient(redisCtx, redis.Config{
		URI:    cfg.RedisURI,
		Prefix: "cdc:",
	}, probeLogger)
	redisCancel()
	if err != nil {
		redisStatus = err.Error()
	} else {
		redisClient.Close()
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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
