// Package runtime wires the supervisor, the workers and the HTTP gateway into
// the two process modes the binary runs in.
//
// The daemon owns process supervision and public HTTP; a worker owns one
// Telegram client and its private socket. Nothing here creates a second
// Telegram client in the gateway.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/editing"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/supervisor"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

// Registry builds the gateway's key registry from enabled clients.
//
// Disabled clients are not registered, so their keys answer 404 rather than
// silently falling back to a different Telegram client.
func Registry(cfg appconfig.Config, build func(appconfig.Client) (gateway.Worker, error)) (gateway.Registry, error) {
	if build == nil {
		return nil, errors.New("runtime: no worker factory")
	}
	workers := make(map[string]gateway.Worker)
	for _, cl := range cfg.Clients {
		if !cl.Enabled {
			continue
		}
		w, err := build(cl)
		if err != nil {
			return nil, fmt.Errorf("runtime: client %q: %w", cl.Key, err)
		}
		if w == nil {
			return nil, fmt.Errorf("runtime: client %q: no worker was built", cl.Key)
		}
		workers[cl.Key] = w
	}
	return registry(workers), nil
}

// Servers is what GET /api/v1/telegram lists: every enabled client, and
// whether a worker serves it. Disabled clients are left out, the same rule the
// registry applies, so a key cannot be selected without being routable.
//
// Only the key and the type cross into the gateway: credentials, endpoints and
// session paths stay in the configuration.
func Servers(cfg appconfig.Config, workers map[string]gateway.Worker) []gateway.ServerInfo {
	out := make([]gateway.ServerInfo, 0, len(cfg.Clients))
	for _, cl := range cfg.Clients {
		if !cl.Enabled {
			continue
		}
		_, registered := workers[cl.Key]
		out = append(out, gateway.ServerInfo{
			Key:          cl.Key,
			Type:         string(cl.Type),
			Enabled:      true,
			Registered:   registered,
			LoginMethods: []string{"phone"},
		})
	}
	return out
}

type registry map[string]gateway.Worker

func (r registry) Worker(key string) (gateway.Worker, bool) {
	w, ok := r[key]
	return w, ok
}

// Supervisor runs the worker processes.
type Supervisor interface {
	Run(ctx context.Context, clients []appconfig.Client) error
}

// AccountSupervisor runs the account-keyed worker lifecycle. The legacy
// Supervisor remains available only to deployments explicitly using the
// configured-client compatibility path.
type AccountSupervisor interface {
	RunAccounts(ctx context.Context, accounts []supervisor.AccountRef) error
}

// DaemonOptions configures the daemon (supervisor + gateway) process.
// BackgroundRunner drains the durable automation queue until its context ends.
//
// It is a separate concern from the supervisor: the supervisor keeps Telegram
// workers alive, while this keeps the work those workers' messages imply
// moving. Both stop together, so a shutdown does not strand a job mid-flight.
type BackgroundRunner interface {
	Run(ctx context.Context) error
}

type DaemonOptions struct {
	Config              appconfig.Config
	Supervisor          Supervisor
	AccountSupervisor   AccountSupervisor
	AccountRunners      []supervisor.AccountRef
	AccountLogins       accountauth.LoginService
	AccountAuthorized   func(context.Context, int64, accountauth.LoginAttempt)
	AccountStatus       func(context.Context, channelstore.Account) string
	StopAccount         func(context.Context, int64, string) error
	ClearAccountSession func(context.Context, int64, channelstore.Account) error
	Workers             map[string]gateway.Worker
	// WorkerRegistry, when set, resolves the current account-keyed workers.
	// Workers remains the static map used by the legacy configured-client path.
	WorkerRegistry gateway.Registry
	// Channels is the local managed-channel store. Nil disables join.
	Channels gateway.ManagedStore
	// Categories is the local category store. Nil disables the local category
	// API; it is the same store as Channels in the daemon.
	Categories gateway.CategoryStore
	// Users is the local account store. Nil disables login and the local user
	// API.
	Users gateway.UserStore
	// Publishes records publishing attempts so a retry cannot post twice.
	Publishes gateway.PublishStore
	// Send accepts and performs direct sends in the background. Nil answers the
	// send routes with 503 and changes nothing else.
	Send *sending.Service
	// Edit accepts and performs full replacements of existing Telegram messages.
	// Nil answers the edit routes with 503 and changes nothing else.
	Edit *editing.Service
	// WordPress is the local WordPress site store. Nil answers the WordPress
	// site routes with 503.
	WordPress gateway.WordPressStore
	// WordPressClient validates a site against its own WordPress REST API. Nil
	// answers the WordPress site routes with 503.
	WordPressClient gateway.WordPressClient
	// Secrets seals WordPress application passwords at rest. Nil answers the
	// WordPress site routes with 503 (and logs one line saying so).
	Secrets gateway.SecretBox
	// WordPressUnconfigured, when set, is why the WordPress key could not be
	// loaded. This is a configuration mistake, not a choice between features:
	// the daemon says so in its log and starts without the WordPress routes.
	// RunDaemon refuses to start at all when it is set and the listener is not
	// loopback, because then the daemon serves other people.
	WordPressUnconfigured error
	// Tokens signs access tokens. Nil disables login.
	Tokens gateway.Tokens
	// Avatars is the persistent avatar cache. Nil serves avatars directly.
	Avatars gateway.AvatarCache
	// Docs renders the Markdown documentation at /docs. Nil answers 404 there.
	Docs http.Handler
	// Panel serves the admin panel's static build under BasePath. Nil leaves
	// the panel's addresses to the API's 404.
	Panel http.Handler
	// AutomationWebhooks, AutomationWorkflows and AutomationStatus serve the
	// background automation area. A nil one makes its routes answer 503.
	AutomationWebhooks  automation.WebhookService
	AutomationWorkflows automation.WorkflowService
	AutomationStatus    automation.AutomationStatusSource
	// AutomationRunner drains the durable background queue. It runs beside the
	// supervisor and stops with it, so automation needs neither an HTTP request
	// nor an open browser to make progress. Nil runs no automation.
	AutomationRunner BackgroundRunner
	// BasePath is the prefix both the API and the panel are served under, as
	// "/admin". Empty serves them at the listener's root.
	BasePath string
	// Listen opens the public listener. Defaults to net.Listen("tcp", addr).
	Listen func(addr string) (net.Listener, error)
	// Logf receives daemon lifecycle messages. Nil discards them.
	Logf func(format string, args ...any)
}

// checkWordPressUnconfigured refuses a non-loopback daemon whose WordPress
// key is broken.
//
// A loopback daemon's operator reads the log on the terminal; a daemon on any
// other address serves people who would only find out from an error body, so
// there a broken key stops the start. The host comes from the configuration's
// parsed ListenHost; a literal configuration that never went through Load has
// none, and then RunDaemon checks the listener's own address instead.
func checkWordPressUnconfigured(api appconfig.APIConfig, unconfigured error) error {
	if unconfigured == nil || api.ListenHost == "" {
		return nil
	}
	return CheckWordPressListener(api, false)
}

// listenHostOf is the host part of addr.
func listenHostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return host
}

// CheckWordPressListener is the loopback rule above on its own, for a caller
// that wants to report it before anything is opened.
//
// A configuration built without ListenHost (a test's literal) has nothing to
// judge, and the daemon has already checked the address when it was loaded.
func CheckWordPressListener(api appconfig.APIConfig, wordPressConfigured bool) error {
	if wordPressConfigured || api.Loopback() || api.ListenHost == "" {
		return nil
	}
	return fmt.Errorf("runtime: api.listen_addr %q is not loopback and the WordPress key is not configured; "+
		"set api.wordpress.encryption_key_env, api.wordpress.encryption_key_path or api.wordpress.encryption_key",
		api.ListenAddr)
}

// RunDaemon serves the public API and supervises workers until ctx ends.
func RunDaemon(ctx context.Context, opts DaemonOptions) error {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	listen := opts.Listen
	if listen == nil {
		listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
	}

	parsed := opts.Config.API
	parsed.ListenHost = listenHostOf(parsed.ListenAddr)
	if err := checkWordPressUnconfigured(parsed, opts.WordPressUnconfigured); err != nil {
		return err
	}
	if opts.Secrets == nil {
		if opts.WordPressUnconfigured != nil {
			logf("daemon: wordpress site routes are disabled: %v", opts.WordPressUnconfigured)
		} else {
			logf("daemon: wordpress site routes are disabled: no encryption key is configured " +
				"(api.wordpress.encryption_key_env, encryption_key_path or encryption_key)")
		}
	}

	handler := gateway.New(gateway.Config{
		Workers: func() gateway.Registry {
			if opts.WorkerRegistry != nil {
				return opts.WorkerRegistry
			}
			return registry(opts.Workers)
		}(),
		Servers:        Servers(opts.Config, opts.Workers),
		RequestTimeout: opts.Config.API.RequestTimeout,
		Operator:       opts.Config.API.OperatorToken(),
		Users:          opts.Users,
		Tokens:         opts.Tokens,
		Channels:       opts.Channels,
		ChannelCreations: func() gateway.ChannelCreationStore {
			store, _ := opts.Channels.(gateway.ChannelCreationStore)
			return store
		}(),
		Categories: opts.Categories,
		// Personal Telegram accounts are the owner of every Telegram resource
		// this API serves, so the gateway gets the account repository and the
		// account-scoped channel store. Without them a keyed route cannot say
		// whose account it would act as, and refuses rather than guessing.
		Accounts: func() gateway.AccountStore {
			store, _ := opts.Channels.(gateway.AccountStore)
			return store
		}(),
		Logins:              opts.AccountLogins,
		AccountAuthorized:   opts.AccountAuthorized,
		AccountStatus:       opts.AccountStatus,
		StopAccount:         opts.StopAccount,
		ClearAccountSession: opts.ClearAccountSession,
		AccountChannels: func() gateway.AccountChannels {
			store, _ := opts.Channels.(gateway.AccountChannels)
			return store
		}(),
		Resolver: func() accountauth.AccountResolver {
			store, ok := opts.Channels.(gateway.AccountStore)
			if !ok {
				return nil
			}
			return gateway.NewStoreAccountResolver(store)
		}(),
		Publishes:       opts.Publishes,
		Send:            opts.Send,
		Edit:            opts.Edit,
		WordPress:       opts.WordPress,
		WordPressClient: opts.WordPressClient,
		Secrets:         opts.Secrets,
		Avatars:         opts.Avatars,
		Docs:            opts.Docs,
		Panel:           opts.Panel,
		BasePath:        opts.BasePath,
		PublicBaseURL:   opts.Config.API.PublicBaseURL,
		// The automation area, and what the status route reports about the
		// subsystem the runner below is draining.
		WebhookAllowLocalhost: opts.Config.Automation.Webhooks.AllowLocalhost,
		AutomationEnabled:     opts.Config.Automation.Enabled,
		AutomationWorkers:     opts.Config.Automation.Workers,
		AutomationWebhooks:    opts.AutomationWebhooks,
		AutomationWorkflows:   opts.AutomationWorkflows,
		AutomationStatus:      opts.AutomationStatus,
		Logf:                  logf,
	})

	ln, err := listen(opts.Config.API.ListenAddr)
	if err != nil {
		return fmt.Errorf("runtime: listen on %s: %w", opts.Config.API.ListenAddr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	supervisorDone := make(chan error, 1)
	if opts.AccountSupervisor != nil {
		go func() { supervisorDone <- opts.AccountSupervisor.RunAccounts(ctx, opts.AccountRunners) }()
	} else if opts.Supervisor != nil {
		go func() { supervisorDone <- opts.Supervisor.Run(ctx, opts.Config.EnabledClients()) }()
	} else {
		supervisorDone <- nil
	}

	// The automation queue runs beside the supervisor and stops with it. This
	// is what makes automation progress with no HTTP request and no open panel:
	// the work lives in the database and is drained here.
	automationDone := make(chan error, 1)
	if opts.AutomationRunner != nil {
		go func() { automationDone <- opts.AutomationRunner.Run(ctx) }()
	} else {
		automationDone <- nil
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	logf("daemon: listening on %s", opts.Config.API.ListenAddr)

	select {
	case err := <-serveErr:
		cancel()
		<-supervisorDone
		<-automationDone
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = srv.Close()
	}
	// The supervisor stops its children after the API stops accepting work.
	if err := <-supervisorDone; err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	// The queue drains to the same deadline: a job already claimed is either
	// finished or released back for the next run, never left half-recorded.
	if err := <-automationDone; err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if shutdownErr != nil {
		return fmt.Errorf("runtime: shutdown: %w", shutdownErr)
	}
	return nil
}

// WorkerOptions configures one worker process.
type WorkerOptions struct {
	Client  appconfig.Client
	Manager WorkerManager
	// Account is the trusted persistence record for an account worker. Nil is
	// the legacy configured-client path. It is never populated from RPC input.
	Account *worker.AccountIdentity
	// Listen opens the worker socket. Defaults to worker.Listen(path).
	Listen func(path string) (net.Listener, error)
	PID    int
	// Logf receives worker lifecycle messages. Nil discards them.
	Logf func(format string, args ...any)
}

// WorkerManager is the one Telegram client a worker owns.
type WorkerManager interface {
	Start(ctx context.Context)
	Status(ctx context.Context) tgapi.Status
	Dialogs(ctx context.Context) ([]tgapi.Channel, error)
	History(ctx context.Context, channel tgapi.Channel, beforeID, limit int) (tgapi.HistoryPage, error)
	DownloadMedia(ctx context.Context, channel tgapi.Channel, messageID int, slice tgapi.MediaSlice) (tgapi.MediaStream, error)
	Message(ctx context.Context, channel tgapi.Channel, messageID int) (tgapi.Message, error)
	FindChannels(ctx context.Context, q tgapi.ChannelQuery) ([]tgapi.ChannelInfo, error)
	ResolveChannel(ctx context.Context, ref tgapi.ChannelRef) (tgapi.ChannelInfo, error)
	JoinChannel(ctx context.Context, ref tgapi.ChannelRef) (tgapi.JoinResult, error)
	DownloadChannelAvatar(ctx context.Context, ref tgapi.ChannelRef) (tgapi.AvatarFile, error)
	DownloadCustomEmoji(ctx context.Context, documentID int64) (tgapi.EmojiFile, error)
	LeaveChannel(ctx context.Context, ref tgapi.ChannelRef) error
	SendMessage(ctx context.Context, ref tgapi.ChannelRef, req tgapi.SendRequest) (tgapi.SendResult, error)
	EditMessage(ctx context.Context, ref tgapi.ChannelRef, req tgapi.EditRequest) (tgapi.EditResult, error)
	DeleteMessage(ctx context.Context, channel tgapi.Channel, messageID int) error
	// Wait blocks until the client has fully stopped, so the worker can flush
	// its session before the process exits.
	Wait(ctx context.Context) error
}

// AccountWorkerManager proves that the Telegram manager uses the same
// account/session identity the worker service was built from.
type AccountWorkerManager interface {
	ValidateAccount(accountID string, remoteUserID int64, serverKey, networkType string) error
}

// RunWorker runs exactly one Telegram client and its private RPC socket until
// ctx ends.
func RunWorker(ctx context.Context, opts WorkerOptions) error {
	workerName := opts.Client.Key
	if opts.Account != nil {
		workerName = opts.Account.ID
	}
	if opts.Manager == nil {
		return fmt.Errorf("runtime: worker %q has no Telegram manager", workerName)
	}
	if opts.Account != nil {
		if err := validateAccountWorker(opts.Client, *opts.Account); err != nil {
			return fmt.Errorf("runtime: account worker %s: %w", opts.Account.ID, err)
		}
		manager, ok := opts.Manager.(AccountWorkerManager)
		if !ok {
			return fmt.Errorf("runtime: account worker %s has a manager without account identity validation",
				opts.Account.ID)
		}
		if err := manager.ValidateAccount(
			opts.Account.ID, opts.Account.RemoteUserID, opts.Account.ServerKey, opts.Account.NetworkType,
		); err != nil {
			return fmt.Errorf("runtime: account worker %s: %w", opts.Account.ID, err)
		}
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	listen := opts.Listen
	if listen == nil {
		listen = worker.Listen
	}

	ln, err := listen(opts.Client.RPCSocketPath)
	if err != nil {
		return fmt.Errorf("runtime: worker %q: %w", workerName, err)
	}
	defer ln.Close()

	var svc *worker.Service
	if opts.Account != nil {
		svc, err = worker.NewServiceForAccount(*opts.Account, opts.Manager, worker.Options{PID: opts.PID},
			worker.WithLogf(logf))
		if err != nil {
			return fmt.Errorf("runtime: account worker %s: %w", opts.Account.ID, err)
		}
	} else {
		svc = worker.NewService(opts.Client, opts.Manager, worker.Options{PID: opts.PID},
			worker.WithLogf(logf))
	}
	handler := rpc.NewHandler(svc)

	clientCtx, stopClient := context.WithCancel(ctx)
	defer stopClient()
	opts.Manager.Start(clientCtx)
	if opts.Account != nil {
		logf("worker: account=%s pid=%d socket=%s started", workerName, opts.PID, opts.Client.RPCSocketPath)
	} else {
		logf("worker: client=%s pid=%d socket=%s started", workerName, opts.PID, opts.Client.RPCSocketPath)
	}

	err = worker.Serve(ctx, ln, handler)
	stopClient()
	// Let gotd finish persisting the session before the process exits.
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = opts.Manager.Wait(waitCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if opts.Account != nil {
		logf("worker: account=%s stopped", workerName)
	} else {
		logf("worker: client=%s stopped", workerName)
	}
	return nil
}

func validateAccountWorker(client appconfig.Client, account worker.AccountIdentity) error {
	if err := account.Valid(); err != nil {
		return err
	}
	if !client.Enabled {
		return fmt.Errorf("the server definition is disabled")
	}
	if client.Key != account.ServerKey {
		return fmt.Errorf("server definition does not match the account record")
	}
	if client.SessionPath != account.SessionRef || filepath.Clean(client.SessionPath) != account.SessionRef {
		return fmt.Errorf("session path does not match the account record")
	}
	if client.RPCSocketPath == "" {
		return fmt.Errorf("account worker has no private socket path")
	}
	switch account.NetworkType {
	case worker.NetworkTelegram:
		if client.Type != appconfig.Official || client.Server != nil {
			return fmt.Errorf("official account network does not match its server definition")
		}
	case worker.NetworkCustom:
		if client.Type != appconfig.Custom || client.Server == nil {
			return fmt.Errorf("custom account network does not match its server definition")
		}
	default:
		return fmt.Errorf("unknown account network type")
	}
	return nil
}
