package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
)

// EnvConfigPath names the multi-instance configuration file.
const EnvConfigPath = "TELESYNC_CONFIG"

// resolveConfigPath decides which file a runtime command uses, and creates it
// on first run.
//
// Precedence: an explicit --config, then TELESYNC_CONFIG, then
// $HOME/.telesync/config.json. Only the default location is created
// automatically; a path the user named (flag or variable) must already exist,
// because silently creating a file at a path someone typed is surprising and
// hides a typo.
func (a *App) resolveConfigPath(explicit string) (string, bool, error) {
	if path := strings.TrimSpace(explicit); path != "" {
		return path, false, nil
	}
	if path := strings.TrimSpace(a.deps.Lookup(EnvConfigPath)); path != "" {
		return path, false, nil
	}

	path, err := a.defaultConfigPath()
	if err != nil {
		return "", false, err
	}
	created, err := appconfig.CreateIfMissing(path)
	if err != nil {
		return "", false, err
	}
	return path, created, nil
}

// defaultConfigPath returns $HOME/.telesync/config.json, honouring the injected
// environment so tests never touch the real home directory.
func (a *App) defaultConfigPath() (string, error) {
	if home := strings.TrimSpace(a.deps.Lookup("HOME")); home != "" {
		return filepath.Join(home, appconfig.DirName, appconfig.FileName), nil
	}
	return appconfig.DefaultPath()
}

// loadConfigPath resolves the path, creates the default file on first run, and
// loads it.
func (a *App) loadConfigPath(explicit string) (appconfig.Config, string, bool, error) {
	path, created, err := a.resolveConfigPath(explicit)
	if err != nil {
		return appconfig.Config{}, "", false, err
	}
	cfg, err := appconfig.Load(path, a.deps.Lookup)
	return cfg, path, created, err
}

// announceCreated tells the user where the configuration was written, on
// stderr so it cannot corrupt `--json` output.
func (a *App) announceCreated(path string) {
	fmt.Fprintf(a.deps.Stderr,
		"created a starter configuration at %s\n"+
			"set app_id in it and export the variables it names, then run this command again\n", path)
}

// runtimeArgs are the parsed arguments of a runtime command.
type runtimeArgs struct {
	Key        string
	AccountID  string
	ConfigPath string
	JSON       bool
	// Timeout bounds the command. Zero means the command's default.
	Timeout time.Duration
}

// defaultAuthTimeout bounds an interactive authorization attempt. Auth-key
// generation alone can take 10-15 s against a slow endpoint, so this is
// generous; it exists so an unreachable network fails instead of hanging.
const defaultAuthTimeout = 5 * time.Minute

// runtimeFlags selects which optional flags a command accepts.
type runtimeFlags struct {
	JSON    bool
	Timeout bool
	Account bool
}

// parseRuntimeArgs accepts --key <client>, --account <account_id>, --config
// <path> and the flags the command declares. Any other argument is a usage
// error, so a typo or an inapplicable flag never silently changes identity.
func (a *App) parseRuntimeArgs(command string, args []string, flags runtimeFlags) (runtimeArgs, int) {
	var out runtimeArgs
	var keyProvided, accountProvided bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--key", "-key":
			keyProvided = true
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "%s: --key needs a client key\n", command)
				return runtimeArgs{}, 2
			}
			out.Key = strings.TrimSpace(args[i+1])
			i++
		case "--account":
			if !flags.Account {
				a.printRuntimeUsage(command)
				return runtimeArgs{}, 2
			}
			if accountProvided {
				fmt.Fprintf(a.deps.Stderr, "%s: --account may only be specified once\n", command)
				return runtimeArgs{}, 2
			}
			accountProvided = true
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "%s: --account needs an account id\n", command)
				return runtimeArgs{}, 2
			}
			out.AccountID = strings.TrimSpace(args[i+1])
			if !validAccountID(out.AccountID) {
				fmt.Fprintf(a.deps.Stderr, "%s: --account must be an opaque account id\n", command)
				return runtimeArgs{}, 2
			}
			i++
		case "--config":
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "%s: --config needs a file path\n", command)
				return runtimeArgs{}, 2
			}
			out.ConfigPath = strings.TrimSpace(args[i+1])
			i++
		case "--json":
			if !flags.JSON {
				a.printRuntimeUsage(command)
				return runtimeArgs{}, 2
			}
			out.JSON = true
		case "--timeout":
			if !flags.Timeout {
				a.printRuntimeUsage(command)
				return runtimeArgs{}, 2
			}
			if i+1 >= len(args) {
				fmt.Fprintf(a.deps.Stderr, "%s: --timeout needs a duration such as 2m\n", command)
				return runtimeArgs{}, 2
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil || d <= 0 {
				fmt.Fprintf(a.deps.Stderr, "%s: --timeout %q must be a positive duration such as 2m\n",
					command, args[i+1])
				return runtimeArgs{}, 2
			}
			out.Timeout = d
			i++
		default:
			a.printRuntimeUsage(command)
			return runtimeArgs{}, 2
		}
	}
	if keyProvided && accountProvided {
		fmt.Fprintf(a.deps.Stderr, "%s: --key and --account are mutually exclusive\n", command)
		return runtimeArgs{}, 2
	}
	return out, 0
}

func validAccountID(id string) bool {
	if len(id) != len("acc_")+26 || !strings.HasPrefix(id, "acc_") {
		return false
	}
	const crockford = "0123456789abcdefghjkmnpqrstvwxyz"
	for i := len("acc_"); i < len(id); i++ {
		if !strings.ContainsRune(crockford, rune(id[i])) {
			return false
		}
	}
	return true
}

func (a *App) printRuntimeUsage(command string) {
	switch command {
	case "config validate":
		fmt.Fprintf(a.deps.Stderr, "usage: telesync config validate [--config <path>] [--json]\n")
	case "daemon":
		fmt.Fprintf(a.deps.Stderr, "usage: telesync daemon [--config <path>]\n")
	case "auth":
		fmt.Fprintf(a.deps.Stderr,
			"usage: telesync auth --key <client> [--config <path>] [--timeout 5m]\n")
	case "admin":
		fmt.Fprintf(a.deps.Stderr,
			"usage: telesync admin [--config <path>]   (asks for the username and password)\n")
	case "worker":
		fmt.Fprintf(a.deps.Stderr,
			"usage: telesync worker (--account <account_id> | --key <client>) [--config <path>]\n")
	default:
		fmt.Fprintf(a.deps.Stderr, "usage: telesync %s --key <client> [--config <path>]\n", command)
	}
}

// runConfigValidate implements "config validate [--json]".
func (a *App) runConfigValidate(_ context.Context, args []string) int {
	parsed, code := a.parseRuntimeArgs("config validate", args, runtimeFlags{JSON: true})
	if code != 0 {
		return code
	}

	cfg, path, created, err := a.loadConfigPath(parsed.ConfigPath)
	if created {
		a.announceCreated(path)
	}
	if err != nil {
		if parsed.JSON {
			out, _ := json.Marshal(map[string]any{
				"valid": false, "error": err.Error(), "config_path": path, "created": created,
			})
			fmt.Fprintf(a.deps.Stdout, "%s\n", out)
		}
		fmt.Fprintf(a.deps.Stderr, "%v\n", err)
		return 1
	}

	if parsed.JSON {
		keys := make([]string, 0, len(cfg.Clients))
		for _, cl := range cfg.Clients {
			keys = append(keys, cl.Key)
		}
		out, _ := json.Marshal(map[string]any{"valid": true, "clients": keys})
		fmt.Fprintf(a.deps.Stdout, "%s\n", out)
		return 0
	}

	fmt.Fprintf(a.deps.Stdout, "configuration is valid\n\n")
	for _, line := range cfg.Describe() {
		fmt.Fprintf(a.deps.Stdout, "  %s\n", line)
	}
	return 0
}

// runDaemon implements "daemon".
func (a *App) runDaemon(ctx context.Context, args []string) int {
	parsed, code := a.parseRuntimeArgs("daemon", args, runtimeFlags{})
	if code != 0 {
		return code
	}
	if a.deps.Runtime.Daemon == nil {
		fmt.Fprintf(a.deps.Stderr, "the runtime is not available in this build\n")
		return 1
	}
	cfg, path, created, err := a.loadConfigPath(parsed.ConfigPath)
	if created {
		a.announceCreated(path)
	}
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "%v\n", err)
		return 1
	}
	if err := a.deps.Runtime.Daemon(ctx, RuntimeRequest{Config: cfg, ConfigPath: path}); err != nil {
		fmt.Fprintf(a.deps.Stderr, "daemon: %v\n", err)
		return 1
	}
	return 0
}

// runWorker implements "worker --key <client>".
func (a *App) runWorker(ctx context.Context, args []string) int {
	parsed, code := a.parseRuntimeArgs("worker", args, runtimeFlags{Account: true})
	if code != 0 {
		return code
	}
	if parsed.Key == "" && parsed.AccountID == "" {
		a.printRuntimeUsage("worker")
		return 2
	}
	if a.deps.Runtime.Worker == nil {
		fmt.Fprintf(a.deps.Stderr, "the runtime is not available in this build\n")
		return 1
	}
	var req RuntimeRequest
	if parsed.AccountID != "" {
		cfg, path, created, err := a.loadConfigPath(parsed.ConfigPath)
		if created {
			a.announceCreated(path)
		}
		if err != nil {
			fmt.Fprintf(a.deps.Stderr, "%v\n", err)
			return 1
		}
		req = RuntimeRequest{ConfigPath: path, Config: cfg, AccountID: parsed.AccountID}
	} else {
		cl, path, exit := a.enabledClient(parsed)
		if exit != 0 {
			return exit
		}
		req = RuntimeRequest{ConfigPath: path, Client: cl}
		// The configuration is best-effort here: the client is already resolved,
		// and a worker must not refuse to run because a reload later failed.
		if cfg, loadErr := appconfig.Load(path, a.deps.Lookup); loadErr == nil {
			req.Config = cfg
		}
	}
	if err := a.deps.Runtime.Worker(ctx, req); err != nil {
		identity := parsed.Key
		if parsed.AccountID != "" {
			identity = parsed.AccountID
		}
		fmt.Fprintf(a.deps.Stderr, "worker %s: %v\n", identity, err)
		return 1
	}
	return 0
}

// runAuth implements "auth --key <client>".
func (a *App) runAuth(ctx context.Context, args []string) int {
	parsed, code := a.parseRuntimeArgs("auth", args, runtimeFlags{Timeout: true})
	if code != 0 {
		return code
	}
	if parsed.Key == "" {
		a.printRuntimeUsage("auth")
		return 2
	}
	if a.deps.Runtime.Login == nil {
		fmt.Fprintf(a.deps.Stderr, "the runtime is not available in this build\n")
		return 1
	}
	cl, path, code := a.enabledClient(parsed)
	if code != 0 {
		return code
	}

	timeout := parsed.Timeout
	if timeout == 0 {
		timeout = defaultAuthTimeout
	}
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, err := a.deps.Runtime.Login(loginCtx, RuntimeRequest{ConfigPath: path, Client: cl})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(a.deps.Stderr,
				"[%s] authentication timed out after %s before the connection became ready; "+
					"check reachability, then retry with a larger --timeout\n", parsed.Key, timeout)
			return 1
		}
		fmt.Fprintf(a.deps.Stderr, "[%s] authentication failed: %v\n", parsed.Key, err)
		return 1
	}
	switch {
	case res.AlreadyAuthorized:
		fmt.Fprintf(a.deps.Stdout, "[%s] already signed in: id=%d username=%s\n",
			parsed.Key, res.UserID, displayName(res.Username))
		return 0
	case res.Authenticated:
		fmt.Fprintf(a.deps.Stdout, "[%s] signed in: id=%d username=%s\n",
			parsed.Key, res.UserID, displayName(res.Username))
		return 0
	default:
		fmt.Fprintf(a.deps.Stderr, "[%s] authentication failed: the session is still not authorized\n", parsed.Key)
		return 1
	}
}

// enabledClient loads the configuration and resolves one enabled client.
func (a *App) enabledClient(args runtimeArgs) (appconfig.Client, string, int) {
	cfg, path, created, err := a.loadConfigPath(args.ConfigPath)
	if created {
		a.announceCreated(path)
	}
	if err != nil {
		fmt.Fprintf(a.deps.Stderr, "%v\n", err)
		return appconfig.Client{}, "", 1
	}
	cl, ok := cfg.Find(args.Key)
	if !ok {
		fmt.Fprintf(a.deps.Stderr, "no client named %q is configured\n", args.Key)
		return appconfig.Client{}, path, 2
	}
	if !cl.Enabled {
		fmt.Fprintf(a.deps.Stderr, "client %q is disabled in the configuration\n", args.Key)
		return appconfig.Client{}, path, 2
	}
	return cl, path, 0
}
