package main

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/publishing"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/cli"
	"github.com/mirza/telesync/internal/daemonlock"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/sending"
	"github.com/mirza/telesync/internal/sessionstore"
	"github.com/mirza/telesync/internal/staging"
)

type notifyWriter struct {
	mu      sync.Mutex
	once    sync.Once
	needle  string
	reached chan struct{}
}

func (w *notifyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	matched := strings.Contains(string(p), w.needle)
	w.mu.Unlock()
	if matched {
		w.once.Do(func() { close(w.reached) })
	}
	return len(p), nil
}

func TestDaemonExclusivelyOwnsItsPersistenceDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := appconfig.Config{
		API: appconfig.APIConfig{
			ListenAddr:     "127.0.0.1:0",
			RequestTimeout: time.Second,
			DocsDir:        appconfig.EmbeddedDocs,
		},
		Runtime: appconfig.RuntimeConfig{
			RunDir:     filepath.Join(dir, "run"),
			SessionDir: filepath.Join(dir, "sessions"),
			DataDir:    filepath.Join(dir, "db"),
			CacheDir:   filepath.Join(dir, "cache"),
			PeerDir:    filepath.Join(dir, "peers"),
		},
		Accounts: appconfig.AccountsConfig{
			SessionKey: []byte("0123456789abcdef0123456789abcdef"),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := &notifyWriter{needle: "daemon: listening on", reached: make(chan struct{})}
	logger := log.New(ready, "", 0)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- runDaemon(ctx, cli.RuntimeRequest{Config: cfg}, logger)
	}()
	select {
	case <-ready.reached:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("first daemon did not reach its listening state")
	}

	blockedCtx, cancelBlocked := context.WithCancel(context.Background())
	cancelBlocked()
	err := runDaemon(blockedCtx, cli.RuntimeRequest{Config: cfg}, log.New(io.Discard, "", 0))
	if !errors.Is(err, daemonlock.ErrAlreadyHeld) {
		t.Fatalf("second daemon error = %v, want %v", err, daemonlock.ErrAlreadyHeld)
	}

	cancel()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first daemon did not stop")
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondReady := &notifyWriter{needle: "daemon: listening on", reached: make(chan struct{})}
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- runDaemon(secondCtx, cli.RuntimeRequest{Config: cfg}, log.New(secondReady, "", 0))
	}()
	select {
	case <-secondReady.reached:
	case <-time.After(5 * time.Second):
		cancelSecond()
		t.Fatal("daemon could not reacquire the persistence directory after shutdown")
	}
	cancelSecond()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("restarted daemon: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restarted daemon did not stop")
	}
}

func TestDaemonRefusesToStartWithoutTheAccountSessionEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runDaemon(ctx, cli.RuntimeRequest{Config: appconfig.Config{
		API: appconfig.APIConfig{
			ListenAddr:     "127.0.0.1:0",
			RequestTimeout: time.Second,
			DocsDir:        appconfig.EmbeddedDocs,
		},
		Runtime: appconfig.RuntimeConfig{
			RunDir:  filepath.Join(dir, "run"),
			DataDir: filepath.Join(dir, "db"),
		},
	}}, log.New(io.Discard, "", 0))
	if !errors.Is(err, secrets.ErrKeyNotSet) {
		t.Fatalf("runDaemon without a session key = %v, want %v", err, secrets.ErrKeyNotSet)
	}
}

func TestAccountAuthorizationPersistsTheSealedSessionBeforeRunnerEligibility(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := channelstore.Open(filepath.Join(dir, "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	owner, err := store.CreateUser(ctx, "Owner", "owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	accountID, err := accountid.New()
	if err != nil {
		t.Fatalf("account id: %v", err)
	}
	if err := store.CreatePending(ctx, owner.ID, accountID, "official", channelstore.NetworkTelegram); err != nil {
		t.Fatalf("create pending account: %v", err)
	}
	encrypter, err := secrets.NewEncrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("create session encrypter: %v", err)
	}
	sessionDir := filepath.Join(dir, "sessions")
	box, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir: sessionDir, AccountID: accountID, Encrypter: encrypter, KeyVersion: 1,
		NetworkKey: "official", NetworkType: sessionstore.NetworkTelegram,
	})
	if err != nil {
		t.Fatalf("create account box: %v", err)
	}
	const session = "authorized-mtproto-session"
	if err := box.StoreSession(ctx, []byte(session)); err != nil {
		t.Fatalf("persist session box: %v", err)
	}

	handoff := newAccountSessionHandoff(store, sessionDir, encrypter, 1)
	if err := handoff.CompletePending(ctx, owner.ID, accountID, accountauth.AuthorizedUser{
		RemoteUserID: 1234,
	}); err != nil {
		t.Fatalf("complete pending account: %v", err)
	}
	record, err := store.ByID(ctx, owner.ID, accountID)
	if err != nil {
		t.Fatalf("read authorized account: %v", err)
	}
	if record.Status != channelstore.AccountStatusConnected {
		t.Fatalf("status = %q, want connected", record.Status)
	}
	if record.Session.Version != secrets.Version1 || record.SessionEncKeyVersion != 1 {
		t.Fatalf("persisted DB session metadata = version %d/key %d",
			record.Session.Version, record.SessionEncKeyVersion)
	}
	opened, err := encrypter.OpenWithAAD(record.Session, sessionstore.AccountAAD(accountID, 1))
	if err != nil {
		t.Fatalf("open DB session box: %v", err)
	}
	if opened != session {
		t.Fatalf("DB session = %q, want the AccountBox session", opened)
	}
	runners, err := store.ListRunnerAccounts(ctx)
	if err != nil {
		t.Fatalf("list runner accounts: %v", err)
	}
	if len(runners) != 1 || runners[0].ID != accountID {
		t.Fatalf("eligible runners = %+v, want only %s", runners, accountID)
	}
}

func TestAccountWorkerMaterialLoadsOnlyTheNamedPersistedAccount(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	store, err := channelstore.Open(filepath.Join(dataDir, "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	owner, err := store.CreateUser(ctx, "Account Owner", "account-owner", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	accountID, err := accountid.New()
	if err != nil {
		t.Fatalf("account id: %v", err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	encrypter, err := secrets.NewEncrypter(key)
	if err != nil {
		t.Fatalf("create session encrypter: %v", err)
	}
	const session = "only-this-account-session"
	sealed, err := encrypter.SealWithAAD(session, sessionstore.AccountAAD(accountID, 1))
	if err != nil {
		t.Fatalf("seal account session: %v", err)
	}
	if _, err := store.Create(ctx, channelstore.Account{
		ID: accountID, OwnerUserID: owner.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, RemoteUserID: 777,
		Status:  channelstore.AccountStatusConnected,
		Session: sealed, SessionEncKeyVersion: 1,
	}); err != nil {
		t.Fatalf("create persisted account: %v", err)
	}

	// AccountPaths enforces the platform Unix-socket limit. Keep this synthetic
	// run path short; no socket is created by the material loader test.
	runDir := filepath.Join(string(filepath.Separator), "r")
	sessionDir := filepath.Join(dir, "sessions")
	peerDir := filepath.Join(dir, "peers")
	legacySession := filepath.Join(sessionDir, "official.json")
	legacySocket := filepath.Join(runDir, "official.sock")
	cfg := appconfig.Config{
		Accounts: appconfig.AccountsConfig{SessionKey: key},
		Runtime: appconfig.RuntimeConfig{
			DataDir: dataDir, RunDir: runDir, SessionDir: sessionDir, PeerDir: peerDir,
		},
		Clients: []appconfig.Client{{
			Key: "official", Type: appconfig.Official, Enabled: true, AppID: 123,
			APIHash: "configured-server-hash", SessionPath: legacySession,
			RPCSocketPath: legacySocket, PeerCachePath: filepath.Join(peerDir, "official.json"),
		}},
	}

	material, err := loadAccountWorker(ctx, cfg, accountID)
	if err != nil {
		t.Fatalf("load account worker material: %v", err)
	}
	if material.Identity.ID != accountID || material.Identity.OwnerUserID != owner.ID ||
		material.Identity.RemoteUserID != 777 || material.Identity.ServerKey != "official" {
		t.Fatalf("worker identity = %+v, want the persisted account identity", material.Identity)
	}
	if material.Client.Key != "official" || material.Client.SessionPath == legacySession ||
		material.Client.RPCSocketPath == legacySocket {
		t.Fatalf("account worker reused a server-key path: %+v", material.Client)
	}
	if filepath.Base(material.Client.SessionPath) != accountID+".box" ||
		filepath.Base(material.Client.RPCSocketPath) != accountID+".sock" ||
		filepath.Base(material.Client.PeerCachePath) != accountID+".json" {
		t.Fatalf("account worker paths are not account-keyed: %+v", material.Client)
	}
	loaded, err := material.Spec.Session.LoadSession(ctx)
	if err != nil {
		t.Fatalf("load sealed account session: %v", err)
	}
	if string(loaded) != session {
		t.Fatalf("loaded session = %q, want the session belonging to %s", loaded, accountID)
	}

	const refreshedSession = "newer-session-flushed-by-the-worker"
	currentBox, err := sessionstore.NewAccountBox(sessionstore.AccountBoxConfig{
		Dir: sessionDir, AccountID: accountID, Encrypter: encrypter, KeyVersion: 1,
		NetworkKey: "official", NetworkType: sessionstore.NetworkTelegram,
	})
	if err != nil {
		t.Fatalf("open current account session: %v", err)
	}
	if err := currentBox.StoreSession(ctx, []byte(refreshedSession)); err != nil {
		t.Fatalf("flush current account session: %v", err)
	}
	material, err = loadAccountWorker(ctx, cfg, accountID)
	if err != nil {
		t.Fatalf("reload worker with its existing session: %v", err)
	}
	loaded, err = material.Spec.Session.LoadSession(ctx)
	if err != nil {
		t.Fatalf("load existing account session: %v", err)
	}
	if string(loaded) != refreshedSession {
		t.Fatalf("worker startup replaced the newer account session with %q", loaded)
	}
	record, err := store.ByID(ctx, owner.ID, accountID)
	if err != nil {
		t.Fatalf("read refreshed account session: %v", err)
	}
	persisted, err := encrypter.OpenWithAAD(record.Session, sessionstore.AccountAAD(accountID, 1))
	if err != nil {
		t.Fatalf("open refreshed DB session: %v", err)
	}
	if persisted != refreshedSession {
		t.Fatalf("DB session = %q, want the latest worker session", persisted)
	}

	unknownID, err := accountid.New()
	if err != nil {
		t.Fatalf("unknown account id: %v", err)
	}
	if _, err := loadAccountWorker(ctx, cfg, unknownID); err == nil {
		t.Fatal("an unknown account id must not fall back to another persisted session")
	}
}

// A background publication is recorded against the account it runs under: the
// idempotency record names one, and the account's permissions are read again at
// execution time so a grant that has since been withdrawn stops the next
// publication. The publisher the daemon builds for the background therefore has
// to carry a resolver.
//
// Without one the interactive route would never reveal the problem — it passes
// its own actor — and every background publish is refused with "this deployment
// cannot resolve the account a background publication runs under".
func TestBackgroundPublisherResolvesTheAccountItPublishesUnder(t *testing.T) {
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	publisher := newBackgroundPublisher(nil, store, nil, t.Logf)
	if publisher.Actors == nil {
		t.Fatal("the background publisher cannot resolve the account it publishes under")
	}
	// The resolver is the store the rest of the pipeline reads, so an account
	// is looked up on every publication rather than carried in from a request.
	if _, found, err := publisher.Actors.GetUser(context.Background(), 1); err != nil {
		t.Fatalf("resolving an account: %v", err)
	} else if found {
		t.Fatal("an empty store resolved an account that does not exist")
	}
}

// sendingWorker is one connection that accepts a send.
type sendingWorker struct {
	sends []rpc.SendRequest
}

func (w *sendingWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{}, errors.New("not used")
}

func (w *sendingWorker) Media(context.Context, int64, int, int, rpc.MediaSlice) (rpc.MediaStream, error) {
	return rpc.MediaStream{}, errors.New("not used")
}

func (w *sendingWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "98", Permissions: rpc.Permissions{New: true, Read: true},
	}}, nil
}

func (w *sendingWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	w.sends = append(w.sends, req)
	return rpc.SendResult{Message: &rpc.Message{ID: 77}, ChannelID: "98"}, nil
}

// TestTheDaemonWiresDirectSendingEndToEnd pins the wiring the daemon performs.
//
// It is a behaviour test rather than a list of non-nil fields, because the way
// this goes wrong is a field that is present in the type and absent in the
// constructor: the send is accepted, queued and performed, and a hole anywhere
// in that chain stops it. The interactive route would not reveal the problem —
// it never reaches the background half.
func TestTheDaemonWiresDirectSendingEndToEnd(t *testing.T) {
	dir := t.TempDir()
	store, err := channelstore.Open(filepath.Join(dir, "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	user, err := store.CreateUser(ctx, "Sender", "sender", "not-a-password-hash", true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "cat", ChannelID: 98, PeerType: channelstore.PeerChannel,
		Title: "Destination", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.ReplacePermissions(ctx, user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{
			channelstore.PermissionContentPublish, channelstore.PermissionChannelsView,
		},
	}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}

	area, err := staging.Open(filepath.Join(dir, "send-staging"), 1<<20)
	if err != nil {
		t.Fatalf("staging.Open: %v", err)
	}
	worker := &sendingWorker{}
	publisher := newBackgroundPublisher(publishing.RegistryFunc(func(key string) (publishing.Worker, bool) {
		return worker, key == "cat"
	}), store, nil, t.Logf)
	enqueuer := queue.NewEnqueuer(store, 5)

	send := newSendService(appconfig.SendConfig{HistoryRetentionDays: 30},
		publisher, store, area, enqueuer, t.Logf)
	if send.Sender == nil || send.Operations == nil || send.Staging == nil || send.Jobs == nil {
		t.Fatal("the daemon's send service is missing part of what a send needs")
	}
	if send.HistoryRetention != 30*24*time.Hour {
		t.Fatalf("retention = %v, want the configured window", send.HistoryRetention)
	}

	authr, err := store.AuthorizerFor(ctx, user.ID)
	if err != nil {
		t.Fatalf("AuthorizerFor: %v", err)
	}

	// A send is background work, and background work is refused unless it names
	// the account it runs under. The account is created here rather than
	// invented as a string so the binding the queue enforces is the real one.
	accountID, err := accountid.New()
	if err != nil {
		t.Fatalf("accountid.New: %v", err)
	}
	if _, err := store.Create(ctx, channelstore.Account{
		ID:          accountID,
		OwnerUserID: user.ID,
		ServerKey:   "cat",
		NetworkType: channelstore.NetworkCustom,
		Status:      channelstore.AccountStatusConnected,
	}); err != nil {
		t.Fatalf("Create account: %v", err)
	}
	actor := publishing.Actor{UserID: user.ID, AccountID: accountID, Auth: authr}

	accepted, err := send.Accept(ctx, actor, sending.AcceptInput{
		ClientKey:            "cat",
		DestinationChannelID: 98,
		IdempotencyKey:       "wiring-001",
		Request:              publishing.DirectSendRequest{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !accepted.Created {
		t.Fatal("a new send was reported as already known")
	}
	if len(worker.sends) != 0 {
		t.Fatal("the message was sent during the request rather than in the background")
	}

	// The job the queue would hand a worker is what performs it.
	if err := send.Execute(ctx, automation.Job{
		EventID: accepted.Operation.PublicID,
		Attempt: 1,
		Owner: automation.Owner{
			OwnerUserID: user.ID,
			AccountID:   accountID,
		},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(worker.sends) != 1 {
		t.Fatalf("%d messages were sent, want 1", len(worker.sends))
	}

	op, found, err := store.SendOperation(ctx, accepted.Operation.PublicID)
	if err != nil || !found {
		t.Fatalf("SendOperation: found=%v err=%v", found, err)
	}
	if op.Status != channelstore.SendSent {
		t.Fatalf("status = %q, want %q", op.Status, channelstore.SendSent)
	}
	if op.RemoteMessageID != 77 {
		t.Fatalf("remote message id = %d, want the one the destination confirmed", op.RemoteMessageID)
	}
}
