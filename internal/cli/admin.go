package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mirza/telesync/internal/channelstore"
)

// AdminRequest is what "telesync admin" hands to its implementation.
type AdminRequest struct {
	// DatabasePath is the local SQLite file the daemon uses (runtime.data_dir).
	DatabasePath string
}

// AdminDeps creates or promotes the administrator account.
//
// It is a dependency rather than logic in this package so the command can be
// tested without a database: the real implementation is in cmd/telesync.
type AdminDeps struct {
	// CreateOrPromote stores the account with a hash of password and every
	// global permission, and returns what it did.
	CreateOrPromote func(ctx context.Context, req AdminRequest, username, fullname, password string) (AdminResult, error)
}

// AdminResult is what the command reports.
type AdminResult struct {
	Username string
	UserID   int64
	// Permissions is the full set the account holds after the command.
	Permissions []channelstore.GlobalPermission
	// Created is false when the account already existed and was promoted.
	Created bool
}

// runAdmin implements "admin [--config <path>]".
//
// It is the supported way to get the first, or another, administrator without
// reaching into SQLite by hand: it takes the credentials interactively (the
// password without echo when stdin is a terminal), stores only a hash, and
// grants the account every global permission. Re-running it for an existing
// username resets that account's password and restores its permissions, which
// is also the recovery path for a forgotten administrator password.
func (a *App) runAdmin(ctx context.Context, args []string) int {
	if a.deps.Runtime.Admin == nil {
		fmt.Fprintf(a.deps.Stderr, "the runtime is not available in this build\n")
		return 1
	}
	var configPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "admin: --config needs a file path\n")
				return 2
			}
			configPath = args[i+1]
			i++
		default:
			a.printRuntimeUsage("admin")
			return 2
		}
	}

	cfg, path, created, err := a.loadConfigPath(configPath)
	if created {
		a.announceCreated(path)
	}
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "%v\n", err)
		return 1
	}

	prompts := a.prompts()
	username, err := prompts.ask("Username: ", false)
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "admin: %v\n", err)
		return 1
	}
	fullname, err := prompts.ask("Full name ["+username+"]: ", false)
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "admin: %v\n", err)
		return 1
	}
	if fullname == "" {
		fullname = username
	}
	password, err := prompts.ask("Password (input hidden): ", true)
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "admin: %v\n", err)
		return 1
	}
	again, err := prompts.ask("Repeat the password: ", true)
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "admin: %v\n", err)
		return 1
	}
	if password != again {
		fmt.Fprintf(a.deps.Stderr, "admin: the passwords do not match\n")
		return 1
	}

	req := AdminRequest{DatabasePath: filepath.Join(cfg.Runtime.DataDir, "channels.db")}
	res, err := a.deps.Runtime.Admin.CreateOrPromote(ctx, req, username, fullname, password)
	if err != nil {
		if errors.Is(err, channelstore.ErrInvalidUser) {
			fmt.Fprintf(a.deps.Stderr, "admin: %v\n", err)
			return 2
		}
		fmt.Fprintf(a.deps.Stderr, "admin: %v\n", err)
		return 1
	}

	if res.Created {
		fmt.Fprintf(a.deps.Stdout, "created administrator %q (id %d)\n", res.Username, res.UserID)
	} else {
		fmt.Fprintf(a.deps.Stdout, "promoted the existing account %q (id %d): its password was reset "+
			"and it now has administrator permissions\n", res.Username, res.UserID)
	}
	names := make([]string, 0, len(res.Permissions))
	for _, p := range res.Permissions {
		names = append(names, string(p))
	}
	fmt.Fprintf(a.deps.Stdout, "  permissions: %v\n", names)
	fmt.Fprintf(a.deps.Stdout, "  log in: curl -s -X POST <api>/api/v1/auth/login -H 'Content-Type: application/json' "+
		"-d '{\"username\":%q,\"password\":\"…\"}' | jq -r .access_token\n", res.Username)
	return 0
}
