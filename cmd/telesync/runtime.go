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
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/ai"
	"github.com/mirza/telesync/internal/automation/events"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/automation/recovery"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/automation/workflows"
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
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/staging"
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
			return runWorkerProcess(ctx, req.Client, req.Config, logger, logw)
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

// newSendService builds the direct-sending pipeline.
//
// Like newBackgroundPublisher it is a function of its own so that a field here
// is written once and can be pinned by a test. Each one is load-bearing: an
// operation that cannot resolve its account, read its staged attachment or
// reach the queue is a message that was promised and will never be sent.
func newSendService(
	cfg appconfig.SendConfig,
	sender *publishing.Service,
	store *channelstore.Store,
	area *staging.Staging,
	jobs *queue.Enqueuer,
	logf func(string, ...any),
) *sending.Service {
	return &sending.Service{
		Sender:           sender,
		Operations:       store,
		Staging:          area,
		Jobs:             jobs,
		HistoryRetention: time.Duration(cfg.HistoryRetentionDays) * 24 * time.Hour,
		Logf:             logf,
	}
}

// runDaemon supervises worker processes and serves the public API.
// newBackgroundPublisher builds the pipeline the background jobs publish
// through. It is a function of its own because one field here is load-bearing
// and easy to drop: a publication is recorded against the account it runs
// under, so the service has to be able to resolve that account again at
// execution time. Without a resolver every background publish is refused with
// "this deployment cannot resolve the account a background publication runs
// under" — the interactive route passes its own actor and would not notice.
func newBackgroundPublisher(
	registry publishing.Registry,
	store *channelstore.Store,
	secrets publishing.SecretBox,
	logf func(string, ...any),
) *publishing.Service {
	return &publishing.Service{
		Workers:    registry,
		Channels:   store,
		Publishes:  store,
		Sites:      store,
		SiteClient: wordpress.NewClient(),
		Secrets:    secrets,
		Actors:     store,
		Logf:       logf,
	}
}

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

	// Background automation. The services are built here because they need the
	// database and the secret box, and both belong to the daemon. With the
	// subsystem switched off nothing is built and the automation routes answer
	// 503 rather than appearing to work.
	// The durable queue is shared by everything that runs in the background:
	// webhook deliveries, workflow runs and direct sends. One queue, one pool of
	// workers and one retry policy, because a deployment should not have to
	// decide twice how much background work may run at once.
	jobHandlers := automation.JobHandlers{}
	enqueuer := queue.NewEnqueuer(store, cfg.Automation.MaxAttempts)

	// The publisher is the v0.0.2 one, called from the background as well as
	// from the route, so the two cannot drift apart. It is what a direct send
	// goes out through too: the destination, its permissions, how a message is
	// built and what a failure means are shared with a publish, and only where
	// the content came from differs.
	registry := publishing.RegistryFunc(func(key string) (publishing.Worker, bool) {
		w, ok := workers[key]
		return w, ok
	})
	publisher := newBackgroundPublisher(registry, store, seal, logger.Printf)

	var (
		autoWebhooks  automation.WebhookService
		autoWorkflows automation.WorkflowService
		autoStatus    automation.AutomationStatusSource
	)
	if cfg.Automation.Enabled {
		if seal == nil {
			return fmt.Errorf("automation is enabled but no encryption key is configured; " +
				"webhook secrets cannot be stored unsealed (api.wordpress.encryption_key_env, " +
				"encryption_key_path or encryption_key)")
		}

		// The ingest socket is how a worker's update stream reaches the
		// database this process owns. It is one socket for the deployment — not
		// one per client — and it is private and local: a worker sends
		// observations and update-state writes over it, and nothing else can
		// reach it. The handler persists and fans out; it never blocks a worker
		// on a delivery or an AI call.
		autoSocket := automationSocketPath(cfg)
		if err := supervisor.RemoveStaleSocket(autoSocket); err != nil {
			return fmt.Errorf("clear the automation socket: %w", err)
		}
		ingestLn, err := worker.Listen(autoSocket)
		if err != nil {
			return fmt.Errorf("listen on the automation socket %s: %w", autoSocket, err)
		}
		ingestHandler := rpc.NewAutomationHandler(
			events.NewRPCIngestBackend(
				events.NewIngestor(store, enqueuer, store, store, store),
				events.LocalIDsFunc(store.LocalIDsByChannelID),
			),
			recovery.NewStateBackend(store),
		)
		go func() {
			if serveErr := worker.Serve(ctx, ingestLn, ingestHandler); serveErr != nil {
				logger.Printf("daemon: automation ingest socket stopped: %v", serveErr)
			}
		}()
		logger.Printf("daemon: automation ingest listening on %s", autoSocket)

		hooks, err := webhooks.New(webhooks.Config{
			PublicBaseURL: cfg.API.PublicBaseURL,
			BasePath:      panelBasePath,
			Webhooks:      cfg.Automation.Webhooks,
			Store:         store,
			Secrets:       seal,
			Events:        store.AutomationEvent,
			// The payload names the channel it came from, so a receiver can tell
			// which channel a message belongs to without calling back into this
			// installation. The labels come from the managed row; the
			// identifiers stay on the event.
			Channels: func(ctx context.Context, managedChannelID int64) (webhooks.Channel, bool, error) {
				ch, found, err := store.GetByID(ctx, managedChannelID)
				if err != nil || !found {
					return webhooks.Channel{}, found, err
				}
				return webhooks.Channel{Type: ch.PeerType, Title: ch.Title, Username: ch.Username}, true, nil
			},
			Jobs:   enqueuer,
			Reopen: enqueuer.Reopen,
		})
		if err != nil {
			return fmt.Errorf("configure webhooks: %w", err)
		}
		autoWebhooks = hooks

		var processor automation.AIProcessor
		if cfg.Automation.AI.Enabled {
			processor, err = ai.New(cfg.Automation.AI)
			if err != nil {
				return fmt.Errorf("configure AI polishing: %w", err)
			}
		}

		engine := &workflows.Handler{
			Workflows: store,
			Runs:      store,
			Events:    store,
			Publisher: publisher,
			AI:        processor,
			Outbound:  store,
			Networks:  networkKeys{config: cfg},
			Workers:   registry,
			Logf:      logger.Printf,
		}
		autoWorkflows = &workflows.Service{Store: store, Terms: publisher, Logf: logger.Printf}
		autoStatus = &workflows.Status{
			Store:   store,
			Enabled: cfg.Automation.Enabled,
			Workers: cfg.Automation.Workers,
		}

		jobHandlers[automation.JobWebhookDelivery] = hooks.Handler()
		jobHandlers[automation.JobWorkflowRun] = engine.HandleJob
		logger.Printf("daemon: automation enabled (%d workers)", cfg.Automation.Workers)
	} else {
		logger.Printf("daemon: automation disabled")
	}

	// Direct sending: a message a caller composed and a file they uploaded,
	// accepted over HTTP and delivered here, long after the request that asked
	// for it has been answered.
	var sendService *sending.Service
	if cfg.Send.Enabled {
		area, err := staging.Open(cfg.Send.StagingDir, cfg.Send.MaxFileBytes)
		if err != nil {
			return fmt.Errorf("open the send staging directory %s: %w", cfg.Send.StagingDir, err)
		}
		sendService = newSendService(cfg.Send, publisher, store, area, enqueuer, logger.Printf)
		jobHandlers[automation.JobTelegramMessageSend] = sendService.Execute

		// A crash can leave an attachment nobody recorded or an operation whose
		// attachment is gone. Both are settled here, at start-up, before any send
		// is accepted — so a caller is never told their message is queued when
		// the bytes it needs are not there.
		report, err := sendService.Cleanup(ctx)
		if err != nil {
			return fmt.Errorf("reconcile the send staging area: %w", err)
		}
		logger.Printf("daemon: direct sending enabled (staging %s, limit %d bytes, "+
			"%d expired operation(s), %d orphaned attachment(s), %d incomplete operation(s), %d partial upload(s))",
			cfg.Send.StagingDir, cfg.Send.MaxFileBytes,
			report.ExpiredOperations, report.AbandonedAttachments,
			report.IncompleteOperations, report.PartialUploads)
	} else {
		logger.Printf("daemon: direct sending disabled")
	}

	var autoRunner runtime.BackgroundRunner
	if len(jobHandlers) > 0 {
		autoRunner = queue.New(store, jobHandlers, queue.Config{
			Workers:         cfg.Automation.Workers,
			PollInterval:    cfg.Automation.PollInterval,
			RetryDelay:      cfg.Automation.RetryDelay,
			Lease:           cfg.Automation.JobLease,
			MaxAttempts:     cfg.Automation.MaxAttempts,
			PublishInterval: cfg.Automation.PublishInterval,
		}, queue.WithLogf(logger.Printf))
	}

	return runtime.RunDaemon(ctx, runtime.DaemonOptions{
		Config:     cfg,
		Supervisor: sup,
		Workers:    workers,
		Channels:   store,
		Categories: store,
		Publishes:  store,
		Send:       sendService,
		Users:      store,
		WordPress:  store,
		// The daemon's own client: public https hosts only, every connection
		// checked at dial time, no redirects, bounded time and body.
		WordPressClient:     wordpress.NewClient(),
		Secrets:             seal,
		Tokens:              tokens,
		Avatars:             cache,
		Docs:                gateway.MountedHandler(docs, panelBasePath),
		Panel:               panelHandler(),
		BasePath:            panelBasePath,
		AutomationWebhooks:  autoWebhooks,
		AutomationWorkflows: autoWorkflows,
		AutomationStatus:    autoStatus,
		AutomationRunner:    autoRunner,
		Listen:              func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) },
		Logf:                logger.Printf,
	})
}

// networkKeys resolves which Telegram-compatible network a client is on.
//
// Loop prevention records a publication against the destination's network, and
// two independent backends can share channel and message numbers, so the
// network has to come from the connection's configuration rather than from its
// name. A client key the configuration does not know has no network and is
// refused rather than guessed.
type networkKeys struct {
	config appconfig.Config
}

// automationSocketPath is where the daemon listens for the workers' automation
// traffic.
//
// It is deliberately not a per-client socket: a worker's own RPC socket is the
// daemon calling into it, while this one runs the other way, and it is shared
// because there is only one database every observation lands in.
func automationSocketPath(cfg appconfig.Config) string {
	return filepath.Join(cfg.Runtime.RunDir, "automation.sock")
}

func (n networkKeys) NetworkKey(clientKey string) (string, bool) {
	cl, ok := n.config.Find(clientKey)
	if !ok {
		return "", false
	}
	return cl.NetworkKey(), true
}

// runWorkerProcess runs exactly one client.
//
// reconnect is the operator's retry interval from runtime.reconnect; it is 0
// when the configuration was not available, which keeps the manager's default.
func runWorkerProcess(ctx context.Context, cl appconfig.Client, cfg appconfig.Config,
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

	// The automation bridge is how this worker's update stream reaches the
	// database the daemon owns. The worker keeps no database of its own, so
	// every observation and every counter write crosses this one private
	// socket. With automation off there is no bridge and the update handler
	// stays the no-op it has always been.
	var automationOptions *gotdclient.AutomationOptions
	if cfg.Automation.Enabled {
		client := rpc.NewAutomationClient(automationSocketPath(cfg), cfg.API.RequestTimeout)
		automationOptions = &gotdclient.AutomationOptions{
			Sink:        client,
			State:       recovery.NewStatePersistence(client),
			Checkpoints: recovery.NewCheckpointPersistence(client),
			NetworkKey:  cl.NetworkKey(),
		}
	}

	manager := gotdclient.NewManager(spec, gotdclient.ManagerOptions{
		Log:       mgrLog,
		Verbose:   verbose,
		Peers:     peers,
		Reconnect: cfg.Runtime.Reconnect,
		// Connection transitions go to the log the daemon captures (stderr).
		// They carry the client key and the endpoint, never a credential.
		OnEvent: func(format string, args ...any) {
			logger.Printf("telegram: "+format, args...)
		},
		Automation: automationOptions,
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
