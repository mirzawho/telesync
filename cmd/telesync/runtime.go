package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/cli"
	"github.com/mirza/telesync/internal/docsite"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/login"
	"github.com/mirza/telesync/internal/panel"
	"github.com/mirza/telesync/internal/peerstore"
	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/runtime"
	"github.com/mirza/telesync/internal/supervisor"
	"github.com/mirza/telesync/internal/wordpress"
	"github.com/mirza/telesync/internal/worker"
)

// runtimeDeps builds the multi-instance runtime commands.
//
// Every Telegram client in this process is either a worker (one client, one
// session) or a short-lived RPC client the gateway forwards to; the daemon
// never creates a Telegram client of its own.
func runtimeDeps(logger *log.Logger, logw io.Writer) cli.RuntimeDeps {
	return cli.RuntimeDeps{
		Daemon: func(ctx context.Context, req cli.RuntimeRequest) error {
			return runDaemon(ctx, req, logger)
		},
		Worker: func(ctx context.Context, req cli.RuntimeRequest) error {
			return runWorkerProcess(ctx, req.Client, req.Config.Runtime.Reconnect, logger, logw)
		},
		Login: func(ctx context.Context, req cli.RuntimeRequest) (login.Result, error) {
			return runAuth(ctx, req.Client, logw)
		},
		Admin: &cli.AdminDeps{CreateOrPromote: createAdministrator},
	}
}

// panelBasePath is where the panel, its assets and the API it calls live: one
// address for everything the browser touches, so the panel's absolute links
// and its server-side calls need no second origin.
const panelBasePath = "/admin"

// panelHandler serves the panel embedded at build time. A binary built around
// only the API carries no panel, and its answers say how to add it.
func panelHandler() http.Handler {
	files := http.FileServerFS(panel.Files())
	if !panelBuilt() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "This binary was built without the admin panel. "+
				"Run `make build` (it builds the panel and folds it in), then restart.")
		})
	}
	return http.StripPrefix(panelBasePath, files)
}

// panelBuilt reports whether the embedded tree holds the panel's page.
func panelBuilt() bool {
	_, err := fs.Stat(panel.Files(), "index.html")
	return err == nil
}

// runDaemon supervises worker processes and serves the public API.
func runDaemon(ctx context.Context, req cli.RuntimeRequest, logger *log.Logger) error {
	cfg := req.Config
	workers := make(map[string]gateway.Worker, len(cfg.Clients))
	for _, cl := range cfg.EnabledClients() {
		workers[cl.Key] = rpc.NewClient(cl.RPCSocketPath, cfg.API.RequestTimeout)
	}

	// The database and the avatar cache belong to the daemon, not to a worker:
	// one process owns them, so the selection and the cache cannot diverge
	// between client processes.
	store, err := channelstore.Open(filepath.Join(cfg.Runtime.DataDir, "channels.db"),
		channelstore.Options{})
	if err != nil {
		return fmt.Errorf("open the channel database: %w", err)
	}
	defer store.Close()

	cache, err := avatarcache.Open(cfg.Runtime.CacheDir, avatarcache.Options{
		TTL:  cfg.Runtime.AvatarTTL,
		Logf: logger.Printf,
	})
	if err != nil {
		return fmt.Errorf("open the avatar cache: %w", err)
	}
	if removed, cleanupErr := cache.Cleanup(ctx); cleanupErr != nil {
		logger.Printf("daemon: avatar cache cleanup: %v", cleanupErr)
	} else if removed > 0 {
		logger.Printf("daemon: avatar cache cleanup removed %d expired entries", removed)
	}

	opts := supervisor.Options{
		ConfigPath:          req.ConfigPath,
		RunDir:              cfg.Runtime.RunDir,
		RestartInitialDelay: cfg.Runtime.RestartInitialDelay,
		RestartMaxDelay:     cfg.Runtime.RestartMaxDelay,
		MaxRestartAttempts:  cfg.Runtime.MaxRestartAttempts,
		StartupTimeout:      cfg.Runtime.StartupTimeout,
		ShutdownTimeout:     cfg.Runtime.ShutdownTimeout,
		ProbeInterval:       250 * time.Millisecond,
		LivenessInterval:    30 * time.Second,
		LivenessFailures:    3,
		Logf:                logger.Printf,
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve the running binary: %w", err)
	}
	opts.BinaryPath = self

	sup := supervisor.New(opts, execLauncher{}, socketProber{timeout: 2 * time.Second})

	// Access tokens are issued by the daemon; a missing key is a startup error
	// rather than a silent fallback to "login does not work".
	var tokens *auth.Tokens
	if cfg.API.JWT.Secret != "" {
		tokens, err = auth.NewTokens(auth.TokenOptions{Secret: []byte(cfg.API.JWT.Secret), TTL: cfg.API.JWT.TTL})
		if err != nil {
			return fmt.Errorf("configure access tokens: %w", err)
		}
	}

	// WordPress site credentials are sealed with a key from the operator's
	// environment. Without one the WordPress routes answer 503: a credential
	// is never stored unsealed.
	var seal gateway.SecretBox
	if enc, err := cfg.API.WordPress.Encrypter(); err != nil {
		logger.Printf("daemon: wordpress sites disabled: %v", err)
	} else {
		seal = enc
	}

	// The rendered documentation: the copy built into the binary, unless
	// api.docs_dir names a tree on disk (the loader has already checked that
	// it exists).
	var docsFS fs.FS
	if cfg.API.DocsDir == appconfig.EmbeddedDocs {
		docsFS = docsite.FS()
	} else {
		docsFS = gateway.OsDocs(cfg.API.DocsDir)
	}
	docs, err := gateway.NewDocsFS(docsFS)
	if err != nil {
		return fmt.Errorf("build the documentation site (api.docs_dir=%q): %w", cfg.API.DocsDir, err)
	}

	return runtime.RunDaemon(ctx, runtime.DaemonOptions{
		Config:     cfg,
		Supervisor: sup,
		Workers:    workers,
		Channels:   store,
		Categories: store,
		Publishes:  store,
		Users:      store,
		WordPress:  store,
		// The daemon's own client: public https hosts only, every connection
		// checked at dial time, no redirects, bounded time and body.
		WordPressClient: wordpress.NewClient(),
		Secrets:         seal,
		Tokens:          tokens,
		Avatars:         cache,
		Docs:            gateway.MountedHandler(docs, panelBasePath),
		Panel:           panelHandler(),
		BasePath:        panelBasePath,
		Listen:          func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) },
		Logf:            logger.Printf,
	})
}

// runWorkerProcess runs exactly one client.
//
// reconnect is the operator's retry interval from runtime.reconnect; it is 0
// when the configuration was not available, which keeps the manager's default.
func runWorkerProcess(ctx context.Context, cl appconfig.Client, reconnect time.Duration,
	logger *log.Logger, logw io.Writer) error {

	spec, err := profile.FromClient(cl)
	if err != nil {
		return err
	}

	verbose := os.Getenv("TELESYNC_DEBUG") != ""
	mgrLog := logw
	if mgrLog != nil && !verbose {
		mgrLog = io.Discard
	}
	// The peer store is what lets a numeric channel id be resolved after a
	// restart: MTProto needs the access hash, which only comes from a prior
	// resolve.
	var peers *peerstore.Store
	if cl.PeerCachePath != "" {
		peers, err = peerstore.Open(cl.PeerCachePath)
		if err != nil {
			return fmt.Errorf("open the peer cache: %w", err)
		}
	}
	manager := gotdclient.NewManager(spec, gotdclient.ManagerOptions{
		Log:       mgrLog,
		Verbose:   verbose,
		Peers:     peers,
		Reconnect: reconnect,
		// Connection transitions go to the log the daemon captures (stderr).
		// They carry the client key and the endpoint, never a credential.
		OnEvent: func(format string, args ...any) {
			logger.Printf("telegram: "+format, args...)
		},
	})

	return runtime.RunWorker(ctx, runtime.WorkerOptions{
		Client:  cl,
		Manager: manager,
		Listen:  worker.Listen,
		PID:     os.Getpid(),
		Logf:    logger.Printf,
	})
}

// runAuth performs the one-time interactive authorization for one client.
func runAuth(ctx context.Context, cl appconfig.Client, logw io.Writer) (login.Result, error) {
	spec, err := profile.FromClient(cl)
	if err != nil {
		return login.Result{}, err
	}
	runner, err := gotdclient.NewLoginForSpec(spec, newTerminalPrompter(), logw)
	if err != nil {
		return login.Result{}, err
	}
	return runner.Login(ctx)
}

// execLauncher starts worker processes with os/exec.
type execLauncher struct{}

func (execLauncher) Start(launch supervisor.Launch) (supervisor.Process, error) {
	if len(launch.Args) == 0 {
		return nil, errors.New("no command to start")
	}
	cmd := exec.Command(launch.Args[0], launch.Args[1:]...)
	// The worker inherits the daemon's environment so app_hash_env and
	// phone_env resolve identically. Output goes to stderr, never stdout.
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}

type execProcess struct{ cmd *exec.Cmd }

func (p *execProcess) Pid() int                   { return p.cmd.Process.Pid }
func (p *execProcess) Wait() error                { return p.cmd.Wait() }
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p *execProcess) Kill() error                { return p.cmd.Process.Kill() }

// socketProber asks a worker's health endpoint over its Unix socket.
type socketProber struct{ timeout time.Duration }

func (p socketProber) Ready(ctx context.Context, socketPath string) error {
	client := rpc.NewClient(socketPath, p.timeout)
	defer client.Close()
	_, err := client.Health(ctx)
	return err
}
