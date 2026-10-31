// Command telesync runs supervised Telegram MTProto clients and serves a
// read-only JSON API that routes to them by configured client key.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mirza/telesync/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// gotd's structured logs go to stderr: they are the only view into a
	// failing handshake, and they contain no credentials. They are opt-in for
	// routine runs because they are very noisy.
	var logw io.Writer
	if os.Getenv("TELESYNC_DEBUG") != "" {
		logw = os.Stderr
	}

	// hash-password prints an Argon2id hash and nothing else; it needs no
	// runtime and no configuration, so it is handled before the CLI is built.
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		if err := runHashPassword(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "hash-password: %v\n", err)
			os.Exit(1)
		}
		return
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)

	app := cli.New(cli.Deps{
		Lookup:  os.Getenv,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Runtime: runtimeDeps(logger, logw),
	})
	os.Exit(app.Run(ctx, os.Args[1:]))
}
