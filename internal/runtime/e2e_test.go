package runtime_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgtest"
	"github.com/gotd/td/transport"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/routing"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/runtime"
	"github.com/mirza/telesync/internal/sessionstore"
)

// pipeDialer is an in-memory listener/dialer pair. Every Dial creates a fresh
// net.Pipe whose server end is handed to Accept, so keep-alive and
// connection-per-request behave exactly as they would over a real socket. The
// sandbox forbids binding sockets; the bytes are identical.
type pipeDialer struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeDialer() *pipeDialer {
	return &pipeDialer{conns: make(chan net.Conn), done: make(chan struct{})}
}

// Dial is the client end.
func (d *pipeDialer) Dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case d.conns <- server:
		return client, nil
	case <-d.done:
		client.Close()
		server.Close()
		return nil, errors.New("pipe dialer closed")
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

// Accept is the server end.
func (d *pipeDialer) Accept() (net.Conn, error) {
	select {
	case c := <-d.conns:
		return c, nil
	case <-d.done:
		return nil, errors.New("pipe dialer closed")
	}
}

func (d *pipeDialer) Close() error {
	d.once.Do(func() { close(d.done) })
	return nil
}

func (d *pipeDialer) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// startMTProtoServer is an in-memory Telegram backend that authorizes one user
// and serves one dialog with two messages.
func startMTProtoServer(t *testing.T) (net.Conn, exchange.PublicKey) {
	t.Helper()

	d := tgtest.NewDispatcher()
	d.HandleFunc(tg.HelpGetConfigRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendResult(req, &tg.Config{
			ThisDC:    req.DC,
			DCOptions: []tg.DCOption{{ID: 2, IPAddress: "mt.backend.test", Port: 8443, Static: true}},
		})
	})
	d.HandleFunc(tg.UsersGetUsersRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendVector(req, &tg.User{ID: 777, AccessHash: 1, Username: "backend_user", FirstName: "Back"})
	})
	d.HandleFunc(tg.MessagesGetDialogsRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendResult(req, &tg.MessagesDialogs{
			Dialogs: []tg.DialogClass{
				&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 500}, TopMessage: 120, UnreadCount: 3, UnreadMentionsCount: 1},
			},
			Chats: []tg.ChatClass{
				&tg.Channel{ID: 500, AccessHash: 900, Title: "کانال نمونه", Username: "sample", Broadcast: true,
					Photo: &tg.ChatPhoto{PhotoID: 55, DCID: 2}},
			},
		})
	})
	d.HandleFunc(tg.MessagesGetHistoryRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		history := &tg.Message{
			ID: 120, Date: 1700000000, Message: "سلام",
			PeerID:  &tg.PeerChannel{ChannelID: 500},
			FromID:  &tg.PeerUser{UserID: 42},
			ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 100},
		}
		history.SetViews(1520)
		chosen := tg.ReactionCount{Reaction: &tg.ReactionEmoji{Emoticon: "❤️"}, Count: 8}
		chosen.SetChosenOrder(1)
		history.SetReactions(tg.MessageReactions{Results: []tg.ReactionCount{
			{Reaction: &tg.ReactionEmoji{Emoticon: "👍"}, Count: 12},
			chosen,
		}})
		err := server.SendResult(req, &tg.MessagesChannelMessages{
			Messages: []tg.MessageClass{history},
			Chats:    []tg.ChatClass{&tg.Channel{ID: 500, AccessHash: 900, Title: "کانال نمونه", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}},
			Users:    []tg.UserClass{&tg.User{ID: 42, FirstName: "علی", Username: "ali"}},
		})
		return err
	})
	d.HandleFunc(tg.ChannelsGetMessagesRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendResult(req, &tg.MessagesChannelMessages{
			Messages: []tg.MessageClass{
				&tg.Message{
					ID: 120, Date: 1700000000, Message: "سلام",
					PeerID:  &tg.PeerChannel{ChannelID: 500},
					FromID:  &tg.PeerUser{UserID: 42},
					ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 100},
					Media:   &tg.MessageMediaPhoto{Photo: &tg.PhotoEmpty{ID: 1}},
				},
			},
			Chats: []tg.ChatClass{&tg.Channel{ID: 500, AccessHash: 900, Title: "کانال نمونه", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}},
			Users: []tg.UserClass{&tg.User{ID: 42, FirstName: "علی", Username: "ali"}},
		})
	})
	d.Fallback(tgtest.HandlerFunc(func(server *tgtest.Server, req *tgtest.Request) error {
		id, _ := req.Buf.PeekID()
		t.Logf("UNHANDLED request type %#x", id)
		return server.SendErr(req, nil)
	}))

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := tgtest.NewServer(exchange.PrivateKey{RSA: rsaKey}, tgtest.UnpackInvoke(d), tgtest.ServerOptions{DC: 2})

	inner, outer := net.Pipe()
	rec := &dialRecorder{inner: inner}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, transport.Listen(newDialListener(rec)))
	}()
	t.Cleanup(func() {
		cancel()
		_ = outer.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("mtproto server did not stop")
		}
	})
	return outer, srv.Key()
}

// dialRecorder accepts exactly one client connection and counts dials.
type dialRecorder struct {
	inner net.Conn
}

// dialListener is a transport listener handing out one connection.
type dialListener struct {
	rec  *dialRecorder
	used bool
	done chan struct{}
}

func newDialListener(rec *dialRecorder) *dialListener {
	return &dialListener{rec: rec, done: make(chan struct{})}
}

func (l *dialListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.rec.inner, nil
	}
	<-l.done
	return nil, errors.New("closed")
}

func (l *dialListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *dialListener) Addr() net.Addr { return l.rec.inner.LocalAddr() }

// specFor builds a custom profile spec whose dialer returns the in-memory
// connection, so the client performs a real MTProto handshake without sockets.
func specFor(t *testing.T, conn net.Conn, key exchange.PublicKey, cl appconfig.Client) *profile.Spec {
	t.Helper()
	store, err := sessionstore.NewAt(cl.SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	resolver := routing.New(routing.Options{
		Host:     "mt.backend.test",
		Port:     8443,
		DCID:     2,
		Policy:   routing.SingleDC,
		Protocol: transport.Intermediate,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return conn, nil
		},
	})
	return &profile.Spec{
		Kind:        profile.Custom,
		AppID:       cl.AppID,
		AppHash:     cl.APIHash,
		SessionPath: store.Path(),
		Session:     store,
		DC:          2,
		Endpoint:    resolver.Endpoint(),
		PublicKeys:  []exchange.PublicKey{key},
		Resolver:    resolver,
		DCList: dcs.List{Options: []tg.DCOption{{
			ID: 2, IPAddress: "mt.backend.test", Port: 8443, Static: true,
		}}},
	}
}

func TestDaemonToWorkerToMTProtoEndToEnd(t *testing.T) {
	mtConn, mtKey := startMTProtoServer(t)
	dir := t.TempDir()
	cl := appconfig.Client{
		Key:           "the-cat",
		Type:          appconfig.Custom,
		Enabled:       true,
		AppID:         1,
		APIHash:       "test-hash",
		SessionPath:   dir + "/sessions/the-cat.json",
		RPCSocketPath: dir + "/run/the-cat.sock",
		Server: &appconfig.Server{
			Host: "mt.backend.test", Port: 8443, DCID: 2,
			Transport: "intermediate", Routing: "single-dc",
		},
	}
	spec := specFor(t, mtConn, mtKey, cl)
	manager := gotdclient.NewManager(spec, gotdclient.ManagerOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Worker side: a real Manager against the in-memory MTProto server,
	// reachable only through this worker's connection.
	workerPipe := newPipeDialer()
	defer workerPipe.Close()
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- runtime.RunWorker(ctx, runtime.WorkerOptions{
			Client:  cl,
			Manager: manager,
			Listen: func(string) (net.Listener, error) {
				return workerPipe, nil
			},
			PID: 4242,
		})
	}()

	// Gateway side: an RPC client over the same in-memory transport.
	upstream := rpc.NewClientWithDialer(workerPipe.Dial, 10*time.Second)
	defer upstream.Close()

	gwPipe := newPipeDialer()
	defer gwPipe.Close()
	gwDone := make(chan error, 1)
	accounts := accountsFor(t, dir)
	accountID := accounts.telegramAccount(t, "the-cat", channelstore.NetworkCustom)
	go func() {
		gwDone <- runtime.RunDaemon(ctx, runtime.DaemonOptions{
			Config: appconfig.Config{
				API:     appconfig.APIConfig{ListenAddr: "127.0.0.1:0", RequestTimeout: 10 * time.Second},
				Clients: []appconfig.Client{cl},
			},
			Supervisor: nil,
			Workers:    map[string]gateway.Worker{accountID: upstream},
			Channels:   accounts.store,
			Users:      accounts.store,
			Tokens:     accounts.tokens,
			Listen: func(string) (net.Listener, error) {
				return gwPipe, nil
			},
		})
	}()

	httpc := accounts.with(&http.Client{
		Transport: &http.Transport{
			DialContext: gwPipe.Dial,
		},
		Timeout: 20 * time.Second,
	})

	// Wait for the client to become authorized in the worker.
	deadline := time.Now().Add(30 * time.Second)
	var health rpc.Health
	var err error
	for time.Now().Before(deadline) {
		health, err = upstream.Health(context.Background())
		if err == nil && health.State == rpc.StateConnected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("worker health: %v", err)
	}
	if health.State != rpc.StateConnected || !health.Authorized {
		t.Fatalf("the worker never became an authorized client: %+v", health)
	}

	// 1. Health through the public API.
	resp, err := httpc.Get("http://gateway/api/v1/telegram/the-cat/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	var healthBody struct {
		Data struct {
			Key        string `json:"key"`
			Type       string `json:"type"`
			State      string `json:"state"`
			Authorized bool   `json:"authorized"`
			Transport  string `json:"transport"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&healthBody); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	if healthBody.Data.State != "connected" || !healthBody.Data.Authorized || healthBody.Data.Type != "custom" {
		t.Fatalf("health = %+v", healthBody.Data)
	}

	// 2. Profile through the public API.
	resp, err = httpc.Get("http://gateway/api/v1/telegram/the-cat/profile")
	if err != nil {
		t.Fatal(err)
	}
	var profileBody struct {
		Data struct {
			Authorized         bool   `json:"authorized"`
			AuthorizationState string `json:"authorization_state"`
			User               *struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profileBody); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile status = %d", resp.StatusCode)
	}
	if !profileBody.Data.Authorized || profileBody.Data.User == nil || profileBody.Data.User.Username != "backend_user" {
		t.Fatalf("profile = %+v", profileBody.Data)
	}

	// 3. Channels through the public API.
	resp, err = httpc.Get("http://gateway/api/v1/telegram/the-cat/channels?page=1&per_page=20")
	if err != nil {
		t.Fatal(err)
	}
	var channelBody struct {
		Data struct {
			Channels []struct {
				ID          int64  `json:"id"`
				Title       string `json:"title"`
				Username    string `json:"username"`
				UnreadCount *int   `json:"unread_count"`
			} `json:"channels"`
		} `json:"data"`
		Meta struct {
			Page    int  `json:"page"`
			Total   int  `json:"total"`
			HasMore bool `json:"has_more"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&channelBody); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("channels status = %d", resp.StatusCode)
	}
	if len(channelBody.Data.Channels) != 1 {
		t.Fatalf("channels = %+v", channelBody.Data.Channels)
	}
	ch := channelBody.Data.Channels[0]
	if ch.ID != 500 || ch.Title != "کانال نمونه" || ch.Username != "sample" {
		t.Fatalf("channel = %+v", ch)
	}
	if ch.UnreadCount == nil || *ch.UnreadCount != 3 {
		t.Fatalf("unread = %v", ch.UnreadCount)
	}
	if channelBody.Meta.Total != 1 || channelBody.Meta.HasMore {
		t.Fatalf("meta = %+v", channelBody.Meta)
	}

	// 4. Messages through the public API, without marking anything read.
	resp, err = httpc.Get("http://gateway/api/v1/telegram/the-cat/channels/500/messages?page=1&per_page=20")
	if err != nil {
		t.Fatal(err)
	}
	var messageBody struct {
		Data struct {
			Channel  *json.RawMessage `json:"channel"`
			Messages []struct {
				ID               int    `json:"id"`
				Text             string `json:"text"`
				ReplyToMessageID int    `json:"reply_to_message_id"`
				Sender           *struct {
					Name string `json:"name"`
				} `json:"sender"`
				Views     *int `json:"views"`
				Reactions []struct {
					Type   string `json:"type"`
					Emoji  string `json:"emoji"`
					Count  int    `json:"count"`
					Chosen *bool  `json:"chosen"`
				} `json:"reactions"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&messageBody); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("messages status = %d", resp.StatusCode)
	}
	if messageBody.Data.Channel != nil {
		t.Fatalf("the channel must not be repeated in a messages body: %s", *messageBody.Data.Channel)
	}
	if len(messageBody.Data.Messages) != 1 {
		t.Fatalf("messages = %+v", messageBody.Data)
	}
	msg := messageBody.Data.Messages[0]
	if msg.ID != 120 || msg.Text != "سلام" || msg.ReplyToMessageID != 100 {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Sender == nil || msg.Sender.Name != "علی" {
		t.Fatalf("sender = %+v", msg.Sender)
	}
	if msg.Views == nil || *msg.Views != 1520 {
		t.Fatalf("views = %v", msg.Views)
	}
	if len(msg.Reactions) != 2 || msg.Reactions[0].Emoji != "👍" || msg.Reactions[0].Count != 12 ||
		msg.Reactions[0].Chosen != nil || msg.Reactions[1].Chosen == nil || !*msg.Reactions[1].Chosen {
		t.Fatalf("reactions = %+v", msg.Reactions)
	}

	// 4b. One message by id, through the same chain.
	resp, err = httpc.Get("http://gateway/api/v1/telegram/the-cat/channels/500/messages/120")
	if err != nil {
		t.Fatal(err)
	}
	var single struct {
		Data struct {
			Messages []struct {
				ID               int    `json:"id"`
				Text             string `json:"text"`
				ReplyToMessageID int    `json:"reply_to_message_id"`
				Media            *struct {
					URL string `json:"url"`
				} `json:"media"`
				Views     *int `json:"views"`
				Reactions []struct {
					Type string `json:"type"`
				} `json:"reactions"`
			} `json:"messages"`
		} `json:"data"`
	}
	rawSingle, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := json.Unmarshal(rawSingle, &single); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("single message status = %d", resp.StatusCode)
	}
	if len(single.Data.Messages) != 1 || single.Data.Messages[0].ID != 120 {
		t.Fatalf("single message = %+v", single.Data.Messages)
	}
	if single.Data.Messages[0].Text != "سلام" || single.Data.Messages[0].ReplyToMessageID != 100 {
		t.Fatalf("single message = %+v", single.Data.Messages[0])
	}
	if !strings.Contains(string(rawSingle), `"views":null`) ||
		!strings.Contains(string(rawSingle), `"reactions":[]`) {
		t.Fatalf("a message with neither views nor reactions must say so: %s", rawSingle)
	}

	// 4c. Whether a field is present at all is lost once decoded into a Go
	// struct, so the raw body is checked too: a message Telegram reported no
	// views for is null, and no reactions is an empty array, never omitted.
	// 5. An unknown key is 404 and never reaches the worker.
	resp, err = httpc.Get("http://gateway/api/v1/telegram/official/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown key = %d", resp.StatusCode)
	}

	// 6. A session file was written for this client only. gotd reports ready
	// just before it writes the session, so poll rather than assume the write
	// has already landed.
	sessionDeadline := time.Now().Add(10 * time.Second)
	for {
		st := sessionstore.Inspect(cl.SessionPath)
		if st.Exists && st.HasAuthKey {
			if filepath.Base(cl.SessionPath) != "the-cat.json" {
				t.Fatalf("session written to the wrong path: %q", cl.SessionPath)
			}
			break
		}
		if time.Now().After(sessionDeadline) {
			t.Fatalf("the worker did not persist its session: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatalf("RunWorker: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
	select {
	case <-gwDone:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
