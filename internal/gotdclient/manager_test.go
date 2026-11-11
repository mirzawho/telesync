package gotdclient_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/exchange"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgtest"
	"github.com/gotd/td/transport"

	"github.com/mirza/telesync/internal/gotdclient"
	"github.com/mirza/telesync/internal/tgapi"
)

// panelServer is the in-memory backend used by the manager tests. It records
// whether a read-history call was made so tests can prove that loading pages
// never marks anything as read.
type panelServer struct {
	mu       sync.Mutex
	readCall bool
}

func (s *panelServer) wasRead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readCall
}

func startPanelServer(t *testing.T, self *tg.User, p *panelServer, extra ...func(*tgtest.Dispatcher)) (*pipeListener, exchange.PublicKey) {
	t.Helper()

	d := tgtest.NewDispatcher()
	d.HandleFunc(tg.HelpGetConfigRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		return server.SendResult(req, &tg.Config{
			ThisDC:    req.DC,
			DCOptions: []tg.DCOption{{ID: 2, IPAddress: "mt.backend.test", Port: 8443, Static: true}},
		})
	})
	d.HandleFunc(tg.UsersGetUsersRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		if self == nil {
			return server.SendErr(req, tgerr.New(401, "UNAUTHORIZED"))
		}
		return server.SendVector(req, self)
	})
	d.HandleFunc(tg.MessagesGetDialogsRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		ch := &tg.Channel{
			ID: 500, AccessHash: 900, Title: "کانال نمونه", Username: "sample",
			Broadcast: true, Photo: &tg.ChatPhoto{PhotoID: 55, DCID: 2},
		}
		return server.SendResult(req, &tg.MessagesDialogs{
			Dialogs: []tg.DialogClass{
				&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 500}, TopMessage: 120, UnreadCount: 7, UnreadMentionsCount: 2},
				&tg.Dialog{Peer: &tg.PeerUser{UserID: 42}, TopMessage: 5},
			},
			Chats: []tg.ChatClass{ch},
			Users: []tg.UserClass{&tg.User{ID: 42, FirstName: "Ali", Username: "ali"}},
		})
	})
	d.HandleFunc(tg.MessagesGetHistoryRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		msg := &tg.Message{
			ID: 120, Date: 1700000000, Message: "سلام",
			PeerID:  &tg.PeerChannel{ChannelID: 500},
			FromID:  &tg.PeerUser{UserID: 42},
			Media:   &tg.MessageMediaPhoto{Photo: &tg.PhotoEmpty{ID: 1}},
			ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 100},
		}
		return server.SendResult(req, &tg.MessagesChannelMessages{
			Messages: []tg.MessageClass{msg},
			Chats:    []tg.ChatClass{&tg.Channel{ID: 500, AccessHash: 900, Title: "کانال نمونه", Broadcast: true, Photo: &tg.ChatPhotoEmpty{}}},
			Users:    []tg.UserClass{&tg.User{ID: 42, FirstName: "علی", Username: "ali"}},
		})
	})
	d.HandleFunc(tg.ChannelsReadHistoryRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
		p.mu.Lock()
		p.readCall = true
		p.mu.Unlock()
		return server.SendResult(req, &tg.BoolTrue{})
	})

	for _, apply := range extra {
		apply(d)
	}

	d.Fallback(tgtest.HandlerFunc(func(server *tgtest.Server, req *tgtest.Request) error {
		id, _ := req.Buf.PeekID()
		t.Logf("UNHANDLED request type %#x", id)
		return tgerr.New(400, "UNHANDLED")
	}))
	_ = p
	return startPanelServerWithDispatcher(t, d)
}

// startPanelServerWithDispatcher wires a dispatcher into an in-memory server.
func startPanelServerWithDispatcher(t *testing.T, d *tgtest.Dispatcher) (*pipeListener, exchange.PublicKey) {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := tgtest.NewServer(exchange.PrivateKey{RSA: rsaKey}, tgtest.UnpackInvoke(d), tgtest.ServerOptions{DC: 2})
	l := newPipeListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := srv.Serve(ctx, transport.Listen(l))
		if err != nil && ctx.Err() == nil {
			t.Logf("tgtest server stopped: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = l.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("server did not stop in time")
		}
	})
	return l, srv.Key()
}

// managerFor builds a Manager over the in-memory server and starts it.
func managerFor(t *testing.T, l *pipeListener, key exchange.PublicKey) *gotdclient.Manager {
	t.Helper()
	rec := &recordedDial{l: l}
	spec := specForCustom(t, l, rec, key)
	m := gotdclient.NewManager(spec, gotdclient.ManagerOptions{Log: os.Stderr})

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx)
	// Cancel and wait for the client goroutine before the test directory is
	// removed: gotd writes the session on shutdown, and deleting that file
	// mid-write made TempDir cleanup flaky.
	t.Cleanup(func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer waitCancel()
		_ = m.Wait(waitCtx)
	})
	return m
}

func waitForState(t *testing.T, m *gotdclient.Manager, want tgapi.State) tgapi.Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last tgapi.Status
	for time.Now().Before(deadline) {
		last = m.Status(context.Background())
		if last.State == want {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state = %q (%s), want %q", last.State, last.Err, want)
	return last
}

func TestManagerReportsReadyWithTheAuthorizedAccount(t *testing.T) {
	self := &tg.User{ID: 777, AccessHash: 1, Username: "me", FirstName: "من"}
	l, key := startPanelServer(t, self, &panelServer{})
	m := managerFor(t, l, key)

	status := waitForState(t, m, tgapi.StateReady)
	if status.User == nil || status.User.Username != "me" || status.User.ID != 777 {
		t.Fatalf("status = %+v", status)
	}
	if status.ThisDC != 2 {
		t.Fatalf("this_dc = %d", status.ThisDC)
	}
}

func TestManagerReportsUnauthorizedWithoutAUser(t *testing.T) {
	l, key := startPanelServer(t, nil, &panelServer{})
	m := managerFor(t, l, key)

	status := waitForState(t, m, tgapi.StateUnauthorized)
	if status.User != nil {
		t.Fatalf("unauthorized status carries a user: %+v", status.User)
	}
	_ = status
}

func TestManagerDialogsOnlyChannelsWithTheirUnreadCount(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	l, key := startPanelServer(t, self, &panelServer{})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	channels, err := m.Dialogs(ctx)
	if err != nil {
		t.Fatalf("Dialogs: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels = %d, want 1 (users are not channels): %+v", len(channels), channels)
	}
	ch := channels[0]
	if ch.ID != 500 || ch.Title != "کانال نمونه" || ch.Username != "sample" {
		t.Fatalf("channel = %+v", ch)
	}
	if ch.UnreadCount == nil || *ch.UnreadCount != 7 {
		t.Fatalf("unread = %v, want 7", ch.UnreadCount)
	}
	if ch.UnreadMentions == nil || *ch.UnreadMentions != 2 {
		t.Fatalf("mentions = %v, want 2", ch.UnreadMentions)
	}
	if !ch.HasPhoto || ch.PhotoID != 55 {
		t.Fatalf("photo = %+v", ch)
	}
	if ch.LastMessageID != 120 {
		t.Fatalf("last message = %d", ch.LastMessageID)
	}
}

func TestManagerHistoryDoesNotMarkAnythingRead(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	p := &panelServer{}
	l, key := startPanelServer(t, self, p)
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	page, err := m.History(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 0, 30)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("messages = %d", len(page.Messages))
	}
	msg := page.Messages[0]
	if msg.ID != 120 || msg.Text != "سلام" {
		t.Fatalf("message = %+v", msg)
	}
	if msg.Media == nil || msg.Media.Type != "photo" || msg.Media.Label != "عکس" {
		t.Fatalf("media = %+v", msg.Media)
	}
	if msg.Sender == nil || msg.Sender.Name != "علی" {
		t.Fatalf("sender = %+v", msg.Sender)
	}
	if msg.Date.UTC() != time.Unix(1700000000, 0).UTC() {
		t.Fatalf("date = %v", msg.Date)
	}
	if p.wasRead() {
		t.Fatal("loading history marked the channel as read")
	}
}

func TestManagerHistoryPreservesVideoMetadataAndGroupedID(t *testing.T) {
	self := &tg.User{ID: 1, Username: "me"}
	video := &tg.Message{ID: 121, Date: 1700000001, Message: "clip",
		PeerID: &tg.PeerChannel{ChannelID: 500}, GroupedID: 9007199254740993,
		Media: &tg.MessageMediaDocument{Document: &tg.Document{MimeType: "video/mp4",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeVideo{Duration: 12, W: 1280, H: 720, SupportsStreaming: true},
			},
		}},
	}
	l, key := startPanelServer(t, self, &panelServer{}, func(d *tgtest.Dispatcher) {
		d.HandleFunc(tg.MessagesGetHistoryRequestTypeID, func(server *tgtest.Server, req *tgtest.Request) error {
			return server.SendResult(req, &tg.MessagesChannelMessages{Messages: []tg.MessageClass{video}})
		})
	})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateReady)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	page, err := m.History(ctx, tgapi.Channel{ID: 500, AccessHash: 900}, 0, 30)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(page.Messages))
	}
	got := page.Messages[0]
	if got.GroupedID != "9007199254740993" {
		t.Fatalf("grouped_id = %q", got.GroupedID)
	}
	if got.Media == nil || got.Media.Type != "video" || got.Media.Duration != 12 ||
		got.Media.Width != 1280 || got.Media.Height != 720 || !got.Media.SupportsStreaming {
		t.Fatalf("media = %+v", got.Media)
	}
}

func TestManagerCallsFailWhenUnauthorized(t *testing.T) {
	l, key := startPanelServer(t, nil, &panelServer{})
	m := managerFor(t, l, key)
	waitForState(t, m, tgapi.StateUnauthorized)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.Dialogs(ctx); !errors.Is(err, tgapi.ErrNotAuthorized) {
		t.Fatalf("Dialogs error = %v, want ErrNotAuthorized", err)
	}
	if _, err := m.History(ctx, tgapi.Channel{ID: 1}, 0, 10); !errors.Is(err, tgapi.ErrNotAuthorized) {
		t.Fatalf("History error = %v, want ErrNotAuthorized", err)
	}
}

func TestManagerRequestsFailFastWhenNeverConnected(t *testing.T) {
	// A backend that never completes the handshake: the manager must answer
	// with a connection error rather than block an HTTP request forever.
	l := newPipeListener()
	rec := &recordedDial{l: l}
	spec := specForCustom(t, l, rec, mustGenerateKey(t))
	m := gotdclient.NewManager(spec, gotdclient.ManagerOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)

	oneShot, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	start := time.Now()
	_, err := m.Dialogs(oneShot)
	if !errors.Is(err, tgapi.ErrNotConnected) {
		t.Fatalf("Dialogs error = %v, want ErrNotConnected", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Dialogs waited %s; the first-connection wait must be bounded", elapsed)
	}
}

func mustGenerateKey(t *testing.T) exchange.PublicKey {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return exchange.PublicKey{RSA: &rsaKey.PublicKey}
}
