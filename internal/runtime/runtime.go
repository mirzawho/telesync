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
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
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
			Key:        cl.Key,
			Type:       string(cl.Type),
			Enabled:    true,
			Registered: registered,
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

// DaemonOptions configures the daemon (supervisor + gateway) process.
type DaemonOptions struct {
	Config     appconfig.Config
	Supervisor Supervisor
	Workers    map[string]gateway.Worker
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
		Workers:         registry(opts.Workers),
		Servers:         Servers(opts.Config, opts.Workers),
		RequestTimeout:  opts.Config.API.RequestTimeout,
		Operator:        opts.Config.API.OperatorToken(),
		Users:           opts.Users,
		Tokens:          opts.Tokens,
		Channels:        opts.Channels,
		Categories:      opts.Categories,
		Publishes:       opts.Publishes,
		WordPress:       opts.WordPress,
		WordPressClient: opts.WordPressClient,
		Secrets:         opts.Secrets,
		Avatars:         opts.Avatars,
		Docs:            opts.Docs,
		Panel:           opts.Panel,
		BasePath:        opts.BasePath,
		Logf:            logf,
	})

	ln, err := listen(opts.Config.API.ListenAddr)
	if err != nil {
		return fmt.Errorf("runtime: listen on %s: %w", opts.Config.API.ListenAddr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	supervisorDone := make(chan error, 1)
	if opts.Supervisor != nil {
		go func() { supervisorDone <- opts.Supervisor.Run(ctx, opts.Config.EnabledClients()) }()
	} else {
		supervisorDone <- nil
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
	if shutdownErr != nil {
		return fmt.Errorf("runtime: shutdown: %w", shutdownErr)
	}
	return nil
}

// WorkerOptions configures one worker process.
type WorkerOptions struct {
	Client  appconfig.Client
	Manager WorkerManager
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
	// Wait blocks until the client has fully stopped, so the worker can flush
	// its session before the process exits.
	Wait(ctx context.Context) error
}

// RunWorker runs exactly one Telegram client and its private RPC socket until
// ctx ends.
func RunWorker(ctx context.Context, opts WorkerOptions) error {
	if opts.Manager == nil {
		return fmt.Errorf("runtime: client %q has no Telegram manager", opts.Client.Key)
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
		return fmt.Errorf("runtime: client %q: %w", opts.Client.Key, err)
	}
	defer ln.Close()

	svc := worker.NewService(opts.Client, opts.Manager, worker.Options{PID: opts.PID},
		worker.WithLogf(logf))
	handler := rpc.NewHandler(svc)

	clientCtx, stopClient := context.WithCancel(ctx)
	defer stopClient()
	opts.Manager.Start(clientCtx)
	logf("worker: client=%s pid=%d socket=%s started", opts.Client.Key, opts.PID, opts.Client.RPCSocketPath)

	err = worker.Serve(ctx, ln, handler)
	stopClient()
	// Let gotd finish persisting the session before the process exits.
	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = opts.Manager.Wait(waitCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logf("worker: client=%s stopped", opts.Client.Key)
	return nil
}
