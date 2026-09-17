package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"

	"conduit/internal/config"
	"conduit/internal/mongo"
	"conduit/internal/redis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// discardLogger is shared by tests that only need a logger for config.Load; the
// CLI commands' own output is captured in the root's Writer instead.
var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// newRootCommandForTest builds the real command tree with its Writer pointed at
// a captured buffer so help output and command output can be asserted. It is
// also used by the integration tests (apikey_integration_test.go) to drive the
// API key subcommands against a live MongoDB. New already installs the empty
// ExitErrHandler, so errors return from Run instead of exiting the test process.
func newRootCommandForTest(t *testing.T) (*cli.Command, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	return New(discardLogger, buf), buf
}

// runRoot runs a freshly built CLI with the given args (the post-binary slice,
// e.g. ["frobnicate"]). It prepends "conduit" for urfave/cli's dispatch, so the
// call mirrors what a real invocation passes as os.Args.
func runRoot(t *testing.T, args []string) ([]byte, error) {
	t.Helper()
	cmd, buf := newRootCommandForTest(t)
	argv := append([]string{"conduit"}, args...)
	err := cmd.Run(context.Background(), argv)
	return buf.Bytes(), err
}

// commandNames returns the names of the given commands, in order.
func commandNames(cmds []*cli.Command) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}

func TestRootCommandRegistration(t *testing.T) {
	root, _ := newRootCommandForTest(t)
	names := commandNames(root.Commands)
	assert.ElementsMatch(t, []string{"start", "health", "apikey"}, names)
}

func TestAPIKeySubcommandRegistration(t *testing.T) {
	root, _ := newRootCommandForTest(t)

	var apikey *cli.Command
	for _, c := range root.Commands {
		if c.Name == "apikey" {
			apikey = c
			break
		}
	}
	require.NotNil(t, apikey, "apikey command must be registered")
	assert.ElementsMatch(t, []string{"create", "ls", "revoke"}, commandNames(apikey.Commands))

	for _, sub := range apikey.Commands {
		switch sub.Name {
		case "create":
			f := requiredFlag(t, sub, "name")
			sf, ok := f.(*cli.StringFlag)
			require.True(t, ok, "name flag must be a *cli.StringFlag")
			assert.True(t, sf.Required, "create must require --name")
		case "revoke":
			// revoke no longer takes an --id flag; it takes a positional <key> args.
			assert.Contains(t, sub.ArgsUsage, "<key>", "revoke help must show positional <key> syntax")
			for _, f := range sub.Flags {
				if f.Names()[0] == "id" {
					t.Fatalf("revoke must not expose an --id flag, found %q", "id")
				}
			}
		}
	}
}

// requiredFlag finds the named flag on a command's flag list.
func requiredFlag(t *testing.T, cmd *cli.Command, name string) cli.Flag {
	t.Helper()
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == name {
				return f
			}
		}
	}
	t.Fatalf("flag %q not found on command %q", name, cmd.Name)
	return nil
}

// Empirical finding (urfave/cli v3.11.0): the root command has no Action (urfave
// injects a default help action), so an unknown first argument is treated as an
// unknown help topic and errors back out of Run. The test asserts only that the
// argument name surfaces, deliberately avoiding the exact help phrasing.
func TestRunUnknownCommand(t *testing.T) {
	_, err := runRoot(t, []string{"frobnicate"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "frobnicate")
}

func TestRunHelp(t *testing.T) {
	out, err := runRoot(t, []string{"--help"})
	require.NoError(t, err)
	help := string(out)
	assert.Contains(t, help, "start")
	assert.Contains(t, help, "health")
	assert.Contains(t, help, "apikey")

	out, err = runRoot(t, []string{"apikey", "--help"})
	require.NoError(t, err)
	help = string(out)
	assert.Contains(t, help, "create")
	assert.Contains(t, help, "ls")
	assert.Contains(t, help, "revoke")
}

// Empirical finding (urfave/cli v3.11.0): running the root command with no
// arguments prints the root help and returns nil.
func TestRunNoArgs(t *testing.T) {
	out, err := runRoot(t, nil)
	require.NoError(t, err)
	assert.Contains(t, string(out), "start")
	assert.Contains(t, string(out), "apikey")
}

func TestStartCommandDispatch(t *testing.T) {
	t.Setenv("MONGODB_URI", "mongodb://dummy:27017")
	t.Setenv("MONGODB_DATABASE", "dummy")
	t.Setenv("REDIS_URI", "redis://dummy:6379")
	t.Setenv("PORT", "9999")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := New(logger, io.Discard)

	sentinel := &sentinelErr{}
	var apiCfg, workerCfg config.Config
	var apiLogger, workerLogger *slog.Logger
	origAPI, origWorker := apiRun, workerRun
	apiRun = func(_ context.Context, cfg config.Config, l *slog.Logger) error {
		apiCfg, apiLogger = cfg, l
		return sentinel
	}
	workerRun = func(_ context.Context, cfg config.Config, l *slog.Logger) error {
		workerCfg, workerLogger = cfg, l
		return nil
	}
	t.Cleanup(func() { apiRun, workerRun = origAPI, origWorker })

	err := root.Run(context.Background(), []string{"conduit", "start"})
	// The first component failure propagates out of the start command, and each
	// component dispatches once with the loaded config and the shared logger.
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, "9999", apiCfg.Port)
	assert.Equal(t, "dummy", workerCfg.MongoDBDatabase)
	assert.Equal(t, "redis://dummy:6379", workerCfg.RedisURI)
	assert.Equal(t, logger, apiLogger)
	assert.Equal(t, apiCfg, workerCfg)
	assert.Equal(t, logger, workerLogger)
}

// TestStartCommandCancellation proves both runtime components receive the same
// derived context and that a caller-cancelled root context flows into them (the
// seam stubs record the context they were dispatched with, mirroring the real
// cmd stack where that context is the process root's signal context).
func TestStartCommandCancellation(t *testing.T) {
	// config.Load runs before dispatch even with stubbed seams, so the required
	// environment must be present or the process boundary os.Exit(1)s.
	t.Setenv("MONGODB_URI", "mongodb://dummy:27017")
	t.Setenv("MONGODB_DATABASE", "dummy")
	t.Setenv("REDIS_URI", "redis://dummy:6379")

	var apiCtx, workerCtx context.Context
	origAPI, origWorker := apiRun, workerRun
	apiRun = func(ctx context.Context, _ config.Config, _ *slog.Logger) error {
		apiCtx = ctx
		return nil
	}
	workerRun = func(ctx context.Context, _ config.Config, _ *slog.Logger) error {
		workerCtx = ctx
		return nil
	}
	t.Cleanup(func() { apiRun, workerRun = origAPI, origWorker })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := New(discardLogger, io.Discard)
	err := root.Run(ctx, []string{"conduit", "start"})
	require.NoError(t, err)
	require.NotNil(t, apiCtx)
	require.NotNil(t, workerCtx)
	assert.ErrorIs(t, apiCtx.Err(), context.Canceled)
	assert.ErrorIs(t, workerCtx.Err(), context.Canceled)
}

type sentinelErr struct{}

func (e *sentinelErr) Error() string { return "sentinel dispatch error" }

// TestNestedCommandUsesRootWriter proves a subcommand's action writes through
// the root command's Writer without any writer being passed to the command
// constructors (urfave/cli inherits the parent's Writer for a subcommand whose
// own Writer is nil). The apikey path is covered end-to-end by the integration
// tests; this test exercises the seam without infrastructure.
func TestNestedCommandUsesRootWriter(t *testing.T) {
	healthEnv(t)
	stubProbes(t, "healthy", "healthy")

	cmd, buf := newRootCommandForTest(t)
	err := cmd.Run(context.Background(), []string{"conduit", "health"})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "MongoDB")
	assert.Contains(t, out, "Redis")
	assert.Contains(t, out, "healthy")
}

// TestRunContextCancellation proves cancellation propagates from the caller
// through Run and the command action into the probe contexts derived by
// runHealth. A pre-cancelled ctx must yield a probe ctx whose Err() is
// context.Canceled — deterministically, with no sleeps.
func TestRunContextCancellation(t *testing.T) {
	healthEnv(t)

	var mongoCtxErr error
	om := mongoProbe
	mongoProbe = func(ctx context.Context, _ mongo.Config) string {
		mongoCtxErr = ctx.Err()
		return "healthy"
	}
	t.Cleanup(func() { mongoProbe = om })
	or := redisProbe
	redisProbe = func(ctx context.Context, _ redis.Config) string {
		return "healthy"
	}
	t.Cleanup(func() { redisProbe = or })

	cmd := New(discardLogger, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cmd.Run(ctx, []string{"conduit", "health"})
	require.NoError(t, err)
	assert.ErrorIs(t, mongoCtxErr, context.Canceled)
}
