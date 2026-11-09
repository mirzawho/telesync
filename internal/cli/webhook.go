package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/webhooktest"
)

// serve-webhook runs the local receiver the delivery engine is tested against.
//
// It is on this command line and not beside it because it belongs to the same
// binary the sender does: the point is to check that what this build sends is
// what a receiver of that build accepts, with no second program to keep in
// step. It starts nothing else — no configuration is read, no database is
// opened, no Telegram client is created — so it runs whatever else this binary
// is able to do.

const serveWebhookUsage = `usage: telesync serve-webhook [--host <addr>] [--port <n>]
                              [--save-response] [--output-dir <dir>]

Runs the local webhook test receiver. It has four endpoints, one per webhook
authentication mode, and each answers with the request it received.

  --host <addr>        listen address (default 127.0.0.1)
  --port <n>           listen port (default 8091)
  --save-response      write one JSON record per request
  --output-dir <dir>   where records go (default ./temp/webhook-responses)

The credentials it verifies against come from the environment:
  TELESYNC_TEST_WEBHOOK_HMAC_SECRET    HMAC-SHA256 secret
  TELESYNC_TEST_WEBHOOK_BEARER_TOKEN   bearer token
  TELESYNC_TEST_WEBHOOK_HEADER_NAME    custom header name
  TELESYNC_TEST_WEBHOOK_HEADER_SECRET  custom header value
`

// serveWebhookArgs are the parsed arguments of "serve-webhook".
type serveWebhookArgs struct {
	Host          string
	Port          int
	SaveResponses bool
	OutputDir     string
	Help          bool
}

// parseServeWebhookArgs reads the receiver's flags. Anything it does not
// recognize is a usage error rather than a silently ignored typo.
func (a *App) parseServeWebhookArgs(args []string) (serveWebhookArgs, int) {
	out := serveWebhookArgs{
		Host:      webhooktest.DefaultHost,
		Port:      webhooktest.DefaultPort,
		OutputDir: webhooktest.DefaultOutputDir,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "serve-webhook: --host needs a listen address\n")
				return serveWebhookArgs{}, 2
			}
			out.Host = strings.TrimSpace(args[i+1])
			i++
		case "--port":
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "serve-webhook: --port needs a port number\n")
				return serveWebhookArgs{}, 2
			}
			port, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil {
				fmt.Fprintf(a.deps.Stderr, "serve-webhook: --port %q is not a port number\n", args[i+1])
				return serveWebhookArgs{}, 2
			}
			out.Port = port
			i++
		case "--save-response":
			out.SaveResponses = true
		case "--output-dir":
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "serve-webhook: --output-dir needs a directory path\n")
				return serveWebhookArgs{}, 2
			}
			out.OutputDir = strings.TrimSpace(args[i+1])
			i++
		case "--help", "-h":
			out.Help = true
		default:
			fmt.Fprintf(a.deps.Stderr, "serve-webhook: unknown argument %q\n\n%s", args[i], serveWebhookUsage)
			return serveWebhookArgs{}, 2
		}
	}
	return out, 0
}

// runServeWebhook implements "serve-webhook".
func (a *App) runServeWebhook(ctx context.Context, args []string) int {
	parsed, code := a.parseServeWebhookArgs(args)
	if code != 0 {
		return code
	}
	if parsed.Help {
		fmt.Fprint(a.deps.Stdout, serveWebhookUsage)
		return 0
	}

	// The credentials are this command's own test values, read before anything
	// binds: a route with no secret configured must never become a route that
	// accepts anything.
	auth, err := webhooktest.LoadAuth(a.deps.Lookup)
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "serve-webhook: %v\n", err)
		return 1
	}

	srv, err := webhooktest.New(webhooktest.Options{
		Host:          parsed.Host,
		Port:          parsed.Port,
		SaveResponses: parsed.SaveResponses,
		OutputDir:     parsed.OutputDir,
		Log:           a.deps.Stdout,
	}, auth)
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "serve-webhook: %v\n", err)
		return 1
	}
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(a.deps.Stderr, "serve-webhook: %v\n", err)
		return 1
	}
	return 0
}
