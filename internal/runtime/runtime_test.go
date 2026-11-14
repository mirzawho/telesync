package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/runtime"
	"github.com/mirza/telesync/internal/supervisor"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

// --- fakes ---

type fakeUpstream struct {
	mu     sync.Mutex
	health rpc.Health
	calls  int
}

func (f *fakeUpstream) Health(context.Context) (rpc.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.health, nil
}

func (f *fakeUpstream) Profile(context.Context) (rpc.Profile, error) {
	return rpc.Profile{Key: f.health.Key}, nil
}

func (f *fakeUpstream) Channels(context.Context, int, int) (rpc.ChannelPage, error) {
	return rpc.ChannelPage{Channels: []rpc.Channel{{ID: 1, Title: "a"}}}, nil
}

func (f *fakeUpstream) Messages(context.Context, int64, int, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{}, nil
}

func (f *fakeUpstream) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	return rpc.MediaStream{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (f *fakeUpstream) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{}, nil
}

func (f *fakeUpstream) Find(context.Context, rpc.FindRequest) (rpc.FindResult, error) {
	return rpc.FindResult{Channels: []rpc.ChannelInfo{}}, nil
}

func (f *fakeUpstream) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: rpc.ChannelInfo{ID: "1"}}, nil
}

func (f *fakeUpstream) Join(context.Context, rpc.ChannelRef) (rpc.JoinResult, error) {
	return rpc.JoinResult{Channel: rpc.ChannelInfo{ID: "1", IsMember: true}, Outcome: rpc.OutcomeJoined}, nil
}

func (f *fakeUpstream) CustomEmoji(context.Context, int64) (rpc.MediaFile, error) {
	return rpc.MediaFile{}, rpc.NewError(rpc.CodeNotFound, "no such emoji", 0)
}

func (f *fakeUpstream) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	return rpc.SendResult{
		Message:   &rpc.Message{ID: 12345, Text: req.Text},
		ChannelID: strconv.FormatInt(req.ChannelID, 10),
	}, nil
}

func (f *fakeUpstream) EditMessage(_ context.Context, req rpc.EditRequest) (rpc.EditResult, error) {
	return rpc.EditResult{Message: &rpc.Message{ID: req.MessageID, Text: req.Text}}, nil
}

func (f *fakeUpstream) DeleteMessage(context.Context, rpc.DeleteMessageRequest) (rpc.DeleteMessageResult, error) {
	return rpc.DeleteMessageResult{Deleted: true}, nil
}

func (f *fakeUpstream) Avatar(context.Context, rpc.ChannelRef) (rpc.AvatarFile, error) {
	return rpc.AvatarFile{MimeType: "image/jpeg", Data: []byte("jpeg")}, nil
}

type fakeSupervisor struct {
	mu      sync.Mutex
	clients []appconfig.Client
	started chan struct{}
	stopped chan struct{}
}

func (f *fakeSupervisor) Run(ctx context.Context, clients []appconfig.Client) error {
	f.mu.Lock()
	f.clients = append([]appconfig.Client(nil), clients...)
	f.mu.Unlock()
	close(f.started)
	<-ctx.Done()
	close(f.stopped)
	return nil
}

type fakeAccountSupervisor struct {
	started chan []supervisor.AccountRef
	stopped chan struct{}
}

func (f *fakeAccountSupervisor) RunAccounts(ctx context.Context, accounts []supervisor.AccountRef) error {
	f.started <- append([]supervisor.AccountRef(nil), accounts...)
	<-ctx.Done()
	close(f.stopped)
	return nil
}

// pipeListener hands out one connection then blocks until closed.
type pipeListener struct {
	c    net.Conn
	used bool
	done chan struct{}
	once sync.Once
}

func newPipeListener(c net.Conn) *pipeListener {
	return &pipeListener{c: c, done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.c, nil
	}
	<-l.done
	return nil, io.EOF
}

func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return l.c.LocalAddr() }

// --- registry ---

func enabledClients(keys ...string) appconfig.Config {
	cfg := appconfig.Config{}
	for _, k := range keys {
		cfg.Clients = append(cfg.Clients, appconfig.Client{
			Key: k, Type: appconfig.Official, Enabled: true,
		})
	}
	return cfg
}

func TestRegistryRegistersOnlyEnabledClients(t *testing.T) {
	cfg := enabledClients("official", "the-cat", "disabled")
	cfg.Clients[2].Enabled = false

	reg, err := runtime.Registry(cfg, func(cl appconfig.Client) (gateway.Worker, error) {
		return &fakeUpstream{health: rpc.Health{Key: cl.Key}}, nil
	})
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	if _, ok := reg.Worker("official"); !ok {
		t.Error("official should be registered")
	}
	if _, ok := reg.Worker("the-cat"); !ok {
		t.Error("the-cat should be registered")
	}
	if _, ok := reg.Worker("disabled"); ok {
		t.Error("a disabled client must not be routable")
	}
	if _, ok := reg.Worker("unknown"); ok {
		t.Error("unknown keys must not resolve")
	}
}

func TestRegistryFailsWhenAWorkerCannotBeBuilt(t *testing.T) {
	cfg := enabledClients("official")
	_, err := runtime.Registry(cfg, func(appconfig.Client) (gateway.Worker, error) {
		return nil, errors.New("cannot build")
	})
	if err == nil {
		t.Fatal("a worker that cannot be built must fail startup")
	}
}

func TestRegistryKeepsClientsApart(t *testing.T) {
	cfg := enabledClients("a", "b")
	reg, err := runtime.Registry(cfg, func(cl appconfig.Client) (gateway.Worker, error) {
		return &fakeUpstream{health: rpc.Health{Key: cl.Key}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := reg.Worker("a")
	b, _ := reg.Worker("b")
	ha, _ := a.Health(context.Background())
	hb, _ := b.Health(context.Background())
	if ha.Key != "a" || hb.Key != "b" {
		t.Fatalf("clients mixed up: %q %q", ha.Key, hb.Key)
	}
}

// --- daemon ---

func TestDaemonStartsAccountRunnersInsteadOfConfiguredClientWorkers(t *testing.T) {
	cfg := enabledClients("official")
	legacy := &fakeSupervisor{started: make(chan struct{}), stopped: make(chan struct{})}
	accounts := &fakeAccountSupervisor{
		started: make(chan []supervisor.AccountRef, 1),
		stopped: make(chan struct{}),
	}
	want := []supervisor.AccountRef{{
		ID: "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa", OwnerUserID: 17,
		ServerKey: "official", NetworkType: supervisor.NetworkTelegram,
		AppID: 123, APIHash: "from-the-configured-server",
	}}
	clientConn, serverConn := net.Pipe()
	ln := newPipeListener(serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.RunDaemon(ctx, runtime.DaemonOptions{
			Config: cfg, Supervisor: legacy,
			AccountSupervisor: accounts, AccountRunners: want,
			Workers: map[string]gateway.Worker{},
			Listen:  func(string) (net.Listener, error) { return ln, nil },
		})
	}()

	select {
	case got := <-accounts.started:
		if len(got) != 1 || got[0] != want[0] {
			cancel()
			t.Fatalf("account runners = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		cancel()
		_ = clientConn.Close()
		t.Fatal("account supervisor was not started")
	}
	select {
	case <-legacy.started:
		cancel()
		t.Fatal("the daemon also started configured-client workers")
	default:
	}

	cancel()
	_ = clientConn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDaemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunDaemon did not stop")
	}
	select {
	case <-accounts.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("account workers were not stopped")
	}
}

func TestDaemonSupervisesAndServesTheGateway(t *testing.T) {
	cfg := enabledClients("official")
	cfg.API.RequestTimeout = time.Second
	accounts := accountsFor(t, t.TempDir())
	accountID := accounts.telegramAccount(t, "official", channelstore.NetworkTelegram)

	sup := &fakeSupervisor{started: make(chan struct{}), stopped: make(chan struct{})}
	clientConn, serverConn := net.Pipe()
	ln := newPipeListener(serverConn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.RunDaemon(ctx, runtime.DaemonOptions{
			Config:     cfg,
			Supervisor: sup,
			Workers: map[string]gateway.Worker{
				accountID: &fakeUpstream{health: rpc.Health{Key: "official", State: rpc.StateConnected}},
			},
			Channels: accounts.store,
			Users:    accounts.store,
			Tokens:   accounts.tokens,
			Listen: func(addr string) (net.Listener, error) {
				if addr != "127.0.0.1:8080" && addr != "" {
					t.Logf("listen addr = %q", addr)
				}
				return ln, nil
			},
		})
	}()

	select {
	case <-sup.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor was never started")
	}
	sup.mu.Lock()
	got := sup.clients
	sup.mu.Unlock()
	if len(got) != 1 || got[0].Key != "official" {
		t.Fatalf("supervisor clients = %+v", got)
	}

	client := accounts.with(&http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return clientConn, nil
		},
		DisableKeepAlives: true,
	}, Timeout: 5 * time.Second})

	resp, err := client.Get("http://gateway/api/v1/telegram/official/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	var body struct {
		Data struct {
			Key   string `json:"key"`
			State string `json:"state"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET health = %d: %s", resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if body.Data.Key != "official" {
		t.Fatalf("response = %d %+v", resp.StatusCode, body.Data)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunDaemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunDaemon did not return")
	}
	select {
	case <-sup.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the supervisor was not stopped")
	}
}

func TestDaemonRefusesToStartWithoutAConfigPathError(t *testing.T) {
	// A listen failure must surface instead of running headless.
	sup := &fakeSupervisor{started: make(chan struct{}), stopped: make(chan struct{})}
	err := runtime.RunDaemon(context.Background(), runtime.DaemonOptions{
		Config:     enabledClients("official"),
		Supervisor: sup,
		Workers:    map[string]gateway.Worker{},
		Listen: func(string) (net.Listener, error) {
			return nil, errors.New("address already in use")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-sup.started:
		t.Fatal("the supervisor must not be started when the API cannot listen")
	default:
	}
}

func TestDaemonDoesNotStartTheSupervisorTwice(t *testing.T) {
	cfg := enabledClients("official")
	sup := &fakeSupervisor{started: make(chan struct{}), stopped: make(chan struct{})}
	clientConn, serverConn := net.Pipe()
	ln := newPipeListener(serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = runtime.RunDaemon(ctx, runtime.DaemonOptions{
			Config:     cfg,
			Supervisor: sup,
			Workers:    map[string]gateway.Worker{},
			Listen:     func(string) (net.Listener, error) { return ln, nil },
		})
	}()
	select {
	case <-sup.started:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor not started")
	}
	sup.mu.Lock()
	n := len(sup.clients)
	sup.mu.Unlock()
	if n != 1 {
		t.Fatalf("supervisor saw %d clients", n)
	}
	_ = clientConn
}

// --- worker process ---

type fakeManager struct {
	mu           sync.Mutex
	started      bool
	status       tgapi.Status
	accountID    string
	remoteUserID int64
	serverKey    string
	networkType  string
}

func (f *fakeManager) ValidateAccount(accountID string, remoteUserID int64, serverKey, networkType string) error {
	if f.accountID != accountID || f.remoteUserID != remoteUserID ||
		f.serverKey != serverKey || f.networkType != networkType {
		return errors.New("fake manager account metadata mismatch")
	}
	return nil
}

func (f *fakeManager) Start(context.Context) {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
}

func (f *fakeManager) Status(context.Context) tgapi.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeManager) Dialogs(context.Context) ([]tgapi.Channel, error) { return nil, nil }
func (f *fakeManager) History(context.Context, tgapi.Channel, int, int) (tgapi.HistoryPage, error) {
	return tgapi.HistoryPage{}, nil
}

func (f *fakeManager) Message(context.Context, tgapi.Channel, int) (tgapi.Message, error) {
	return tgapi.Message{}, tgapi.ErrMessageNotFound
}

func (f *fakeManager) DownloadMedia(context.Context, tgapi.Channel, int, tgapi.MediaSlice) (tgapi.MediaStream, error) {
	return tgapi.MediaStream{}, tgapi.ErrMediaNotFound
}

func (f *fakeManager) FindChannels(context.Context, tgapi.ChannelQuery) ([]tgapi.ChannelInfo, error) {
	return nil, nil
}

func (f *fakeManager) ResolveChannel(context.Context, tgapi.ChannelRef) (tgapi.ChannelInfo, error) {
	return tgapi.ChannelInfo{}, tgapi.ErrChannelNotFound
}

func (f *fakeManager) JoinChannel(context.Context, tgapi.ChannelRef) (tgapi.JoinResult, error) {
	return tgapi.JoinResult{}, tgapi.ErrChannelNotFound
}

func (f *fakeManager) DownloadCustomEmoji(context.Context, int64) (tgapi.EmojiFile, error) {
	return tgapi.EmojiFile{}, tgapi.ErrCustomEmojiNotFound
}

func (f *fakeManager) DownloadChannelAvatar(context.Context, tgapi.ChannelRef) (tgapi.AvatarFile, error) {
	return tgapi.AvatarFile{}, tgapi.ErrNoAvatar
}

func (f *fakeManager) LeaveChannel(context.Context, tgapi.ChannelRef) error {
	return tgapi.ErrNotAMember
}

func (f *fakeManager) SendMessage(context.Context, tgapi.ChannelRef, tgapi.SendRequest) (tgapi.SendResult, error) {
	return tgapi.SendResult{}, nil
}

func (f *fakeManager) EditMessage(context.Context, tgapi.ChannelRef, tgapi.EditRequest) (tgapi.EditResult, error) {
	return tgapi.EditResult{}, nil
}

func (f *fakeManager) DeleteMessage(context.Context, tgapi.Channel, int) error { return nil }

func (f *fakeManager) Wait(context.Context) error { return nil }

func TestRunWorkerStartsTheClientAndServesRPC(t *testing.T) {
	manager := &fakeManager{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2, User: &tgapi.Account{ID: 1},
	}}
	clientConn, serverConn := net.Pipe()
	ln := newPipeListener(serverConn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.RunWorker(ctx, runtime.WorkerOptions{
			Client:  appconfig.Client{Key: "official", Type: appconfig.Official},
			Manager: manager,
			Listen:  func(string) (net.Listener, error) { return ln, nil },
			PID:     99,
		})
	}()

	upstream := rpc.NewClientWithDialer(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}, 2*time.Second)
	defer upstream.Close()

	var health rpc.Health
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		health, err = upstream.Health(context.Background())
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Health over the worker RPC: %v", err)
	}
	if health.Key != "official" || health.PID != 99 || health.State != rpc.StateConnected {
		t.Fatalf("health = %+v", health)
	}
	manager.mu.Lock()
	started := manager.started
	manager.mu.Unlock()
	if !started {
		t.Fatal("the Telegram client was never started")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWorker: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWorker did not return")
	}
}

func TestRunWorkerBuildsTheServiceFromTrustedAccountIdentity(t *testing.T) {
	const accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	identity := worker.AccountIdentity{
		ID:           accountID,
		OwnerUserID:  7,
		ServerKey:    "official",
		NetworkType:  "telegram",
		RemoteUserID: 1007,
		SessionRef:   filepath.Join("/sessions", accountID+".box"),
	}
	manager := &fakeManager{
		status:       tgapi.Status{State: tgapi.StateReady, ThisDC: 2, User: &tgapi.Account{ID: 1007}},
		accountID:    identity.ID,
		remoteUserID: identity.RemoteUserID,
		serverKey:    identity.ServerKey,
		networkType:  identity.NetworkType,
	}
	clientConn, serverConn := net.Pipe()
	ln := newPipeListener(serverConn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runtime.RunWorker(ctx, runtime.WorkerOptions{
			Client: appconfig.Client{
				Key: "official", Type: appconfig.Official, Enabled: true,
				SessionPath: identity.SessionRef, RPCSocketPath: "/run/" + accountID + ".sock",
			},
			Account: &identity,
			Manager: manager,
			Listen:  func(string) (net.Listener, error) { return ln, nil },
			PID:     101,
		})
	}()

	upstream := rpc.NewClientWithDialer(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}, 2*time.Second)
	defer upstream.Close()

	var health rpc.Health
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		health, err = upstream.Health(context.Background())
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Health over the account worker RPC: %v", err)
	}
	if health.AccountID != accountID || health.OwnerUserID != identity.OwnerUserID {
		t.Fatalf("health identity = %q/%d, want %q/%d",
			health.AccountID, health.OwnerUserID, accountID, identity.OwnerUserID)
	}
	if health.Key != identity.ServerKey || health.Type != string(appconfig.Official) {
		t.Fatalf("health server metadata = %q/%q", health.Key, health.Type)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWorker: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWorker did not return")
	}
}

func TestRunWorkerRejectsAccountMetadataThatDoesNotMatchTheServerDefinition(t *testing.T) {
	const accountID = "acc_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	identity := worker.AccountIdentity{
		ID:           accountID,
		OwnerUserID:  7,
		ServerKey:    "another-server",
		NetworkType:  "telegram",
		RemoteUserID: 1007,
		SessionRef:   filepath.Join("/sessions", accountID+".box"),
	}
	listenCalled := false
	err := runtime.RunWorker(context.Background(), runtime.WorkerOptions{
		Client: appconfig.Client{
			Key: "official", Type: appconfig.Official, Enabled: true,
			SessionPath: identity.SessionRef, RPCSocketPath: "/run/" + accountID + ".sock",
		},
		Account: &identity,
		Manager: &fakeManager{},
		Listen: func(string) (net.Listener, error) {
			listenCalled = true
			return nil, errors.New("must not listen")
		},
	})
	if err == nil {
		t.Fatal("an account bound to a different server definition must not start")
	}
	if listenCalled {
		t.Fatal("the worker opened a socket before validating account metadata")
	}
}

func TestRunWorkerReportsAStartupFailure(t *testing.T) {
	manager := &fakeManager{}
	err := runtime.RunWorker(context.Background(), runtime.WorkerOptions{
		Client:  appconfig.Client{Key: "official", Type: appconfig.Official},
		Manager: manager,
		Listen: func(string) (net.Listener, error) {
			return nil, errors.New("socket path exists and is not a socket")
		},
	})
	if err == nil {
		t.Fatal("a listen failure must be reported")
	}
}

func TestServersListsEnabledClientsOnlyAndNeverTheirConfiguration(t *testing.T) {
	cfg := appconfig.Config{Clients: []appconfig.Client{
		{Key: "official", Type: appconfig.Official, Enabled: true},
		{Key: "the-cat", Type: appconfig.Custom, Enabled: true,
			APIHash: "secret-hash", Phone: "+15550001111",
			Server: &appconfig.Server{Host: "mtproto.example.invalid", Port: 443, DCID: 2}},
		{Key: "retired", Type: appconfig.Official, Enabled: false},
	}}
	workers := map[string]gateway.Worker{"official": &fakeUpstream{}}

	got := runtime.Servers(cfg, workers)
	if len(got) != 2 {
		t.Fatalf("servers = %+v, want the two enabled clients", got)
	}
	if got[0].Key != "official" || !got[0].Registered || got[0].Type != "official" {
		t.Fatalf("official = %+v", got[0])
	}
	// Enabled but with no worker built: still selectable, health unknown.
	if got[1].Key != "the-cat" || got[1].Registered || got[1].Type != "custom" {
		t.Fatalf("the-cat = %+v", got[1])
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"secret-hash", "+15550001111", "mtproto.example.invalid"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("the server list carries %q: %s", leak, encoded)
		}
	}
}
