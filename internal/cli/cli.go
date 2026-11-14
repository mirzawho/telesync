// Package cli implements the telesync command line.
//
// The runtime commands are daemon, worker, auth and config validate, plus
// serve-webhook, a local receiver the delivery engine is tested against.
// Nothing else is exposed: telesync is a supervised MTProto client runtime, not
// a messaging client.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/login"
)

// Deps are the CLI's external dependencies.
type Deps struct {
	Lookup func(string) string
	// Stdin is where interactive prompts read; nil means os.Stdin.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Runtime, when set, enables the runtime commands.
	Runtime RuntimeDeps
}

// RuntimeRequest is what a runtime command hands to its implementation.
type RuntimeRequest struct {
	// ConfigPath is the file the configuration was loaded from. The daemon
	// passes it to every worker it starts.
	ConfigPath string
	// Config is the validated configuration (daemon and worker), so the daemon
	// and the worker it starts share every runtime setting.
	Config appconfig.Config
	// Client is the resolved, enabled client (worker and auth only).
	Client appconfig.Client
	// AccountID is the account identity for an account worker. It is the only
	// account-related process argument; owner and server metadata are loaded
	// from trusted persistence/configuration by the worker runtime.
	AccountID string
}

// RuntimeDeps are the runtime command implementations. A nil field disables
// that command with a clear message instead of a panic.
type RuntimeDeps struct {
	// Daemon runs the supervisor and the public HTTP API until ctx ends.
	Daemon func(ctx context.Context, req RuntimeRequest) error
	// Worker runs one worker process for the requested client.
	Worker func(ctx context.Context, req RuntimeRequest) error
	// Login authorizes one configured client interactively.
	Login func(ctx context.Context, req RuntimeRequest) (login.Result, error)
	// Admin creates or promotes the local administrator account. Nil disables
	// the command with a clear message.
	Admin *AdminDeps
}

// App is the telesync CLI.
type App struct {
	deps Deps
	// tty overrides "is stdin a terminal" (tests).
	tty func() bool
}

// New creates an App.
func New(deps Deps) *App {
	if deps.Stdout == nil {
		deps.Stdout = io.Discard
	}
	if deps.Stderr == nil {
		deps.Stderr = io.Discard
	}
	return &App{deps: deps}
}

const usage = `telesync — multi-instance Telegram MTProto runtime

Runs one worker process per configured Telegram client and serves a read-only
JSON HTTP API that routes to the right worker by client key.

Usage:
  telesync daemon [--config <path>]
      Start the supervisor and the public HTTP API.

  telesync worker --account <account_id> [--config <path>]
      Run one account worker process. The daemon starts these; it owns one
      account session, peer cache and private socket.

  telesync worker --key <client> [--config <path>]
      Legacy configured-client worker mode during migration.

  telesync auth --key <client> [--config <path>] [--timeout 5m]
      Run the one-time interactive authorization for one client and save its
      session.

  telesync config validate [--config <path>] [--json]
      Validate the configuration file without connecting.

  telesync hash-password
      Read a password from stdin and print an Argon2id hash of it.

  telesync admin [--config <path>]
      Create an administrator account, or reset an existing one: it asks for
      the username and password (hidden), stores only a hash, and grants every
      global permission. This is the way to get the first account.

  telesync serve-webhook [--host <addr>] [--port <n>] [--save-response]
                         [--output-dir <dir>]
      Run the local webhook test receiver: four endpoints, one per webhook
      authentication mode, each answering with the request it received. It
      reads no configuration, opens no database and connects no Telegram
      client; its test credentials come from the environment. This is a
      development tool for checking deliveries against a receiver of the same
      build, not a production endpoint.

  telesync help
      Show this help.

The configuration file is $HOME/.telesync/config.json, or whatever --config or
TELESYNC_CONFIG names. A missing default file is created on first run; a path you
name yourself must already exist. The file references secrets by
environment-variable name; see docs/CONFIGURATION.md.
`

// Run executes the command line and returns the process exit code.
//
// A bare `telesync` prints usage and makes sure a configuration file exists:
// it is the first thing most users run, and leaving them with no file and no
// instruction is a dead end.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.reportConfig()
		fmt.Fprint(a.deps.Stdout, usage)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(a.deps.Stdout, usage)
		return 0
	case "config":
		if len(args) > 1 && args[1] == "validate" {
			return a.runConfigValidate(ctx, args[2:])
		}
		fmt.Fprintf(a.deps.Stderr, "usage: telesync config validate [--config <path>] [--json]\n")
		return 2
	case "daemon":
		return a.runDaemon(ctx, args[1:])
	case "worker":
		return a.runWorker(ctx, args[1:])
	case "auth":
		return a.runAuth(ctx, args[1:])
	case "admin":
		return a.runAdmin(ctx, args[1:])
	case "serve-webhook":
		return a.runServeWebhook(ctx, args[1:])
	default:
		fmt.Fprintf(a.deps.Stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// reportConfig prints where the configuration lives, creating the default
// starter on first run. Errors never change the exit code: this is guidance
// printed alongside usage, not a command that failed.
func (a *App) reportConfig() {
	path, created, err := a.resolveConfigPath("")
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "configuration: %v\n", err)
		return
	}
	if _, statErr := os.Stat(path); statErr != nil {
		// An explicitly named file that does not exist: say so, and do not
		// create it (see resolveConfigPath).
		fmt.Fprintf(a.deps.Stderr, "configuration: %v\n", statErr)
		return
	}
	if created {
		// stdout: the user asked for nothing, so this is part of the answer,
		// not a warning about a command that failed.
		fmt.Fprintf(a.deps.Stdout,
			"created a starter configuration at %s\n"+
				"set app_id in it and export the variables it names, then run `telesync daemon`\n\n", path)
		return
	}
	fmt.Fprintf(a.deps.Stdout, "configuration: %s\n\n", path)
}

func displayName(username string) string {
	if username == "" {
		return "(none)"
	}
	return username
}
