package rpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/rpc"
)

// fakeBackend records calls and returns scripted answers.
type fakeBackend struct {
	health    rpc.Health
	profile   rpc.Profile
	profileE  error
	channels  rpc.ChannelPage
	chanErr   error
	messages  rpc.MessagePage
	msgErr    error
	media     []byte
	mediaName string
	mediaType string
	mediaSize int64
	mediaBody io.ReadCloser
	mediaE    error
	gotSlice  rpc.MediaSlice
	message   rpc.MessagePage
	messageE  error

	gotPage, gotPerPage int
	gotChannel          int64
	calls               int

	gotMediaChannel int64
	gotMediaMessage int

	customEmoji    rpc.MediaFile
	customEmojiE   error
	gotCustomEmoji int64

	gotMessageChannel int64
	gotMessageID      int

	find     rpc.FindResult
	findErr  error
	gotFind  rpc.FindRequest
	channel  rpc.ChannelResult
	chanInfo error
	gotRef   rpc.ChannelRef
	join     rpc.JoinResult
	joinErr  error
	avatar   rpc.AvatarFile
	avatarE  error
	leave    rpc.LeaveResult
	leaveE   error
	gotOp    string

	send      rpc.SendResult
	sendE     error
	gotSend   rpc.SendRequest
	edit      rpc.EditResult
	editE     error
	gotEdit   rpc.EditRequest
	deleted   rpc.DeleteMessageResult
	deleteE   error
	gotDelete rpc.DeleteMessageRequest
}

func (f *fakeBackend) Health(context.Context) rpc.Health { return f.health }

func (f *fakeBackend) Profile(context.Context) (rpc.Profile, error) {
	return f.profile, f.profileE
}

func (f *fakeBackend) Channels(_ context.Context, page, perPage int) (rpc.ChannelPage, error) {
	f.calls++
	f.gotPage, f.gotPerPage = page, perPage
	return f.channels, f.chanErr
}

func (f *fakeBackend) Messages(_ context.Context, channelID int64, page, perPage int) (rpc.MessagePage, error) {
	f.gotChannel, f.gotPage, f.gotPerPage = channelID, page, perPage
	return f.messages, f.msgErr
}

func (f *fakeBackend) Message(_ context.Context, channelID int64, messageID int) (rpc.MessagePage, error) {
	f.gotMessageChannel, f.gotMessageID = channelID, messageID
	return f.message, f.messageE
}

func (f *fakeBackend) Media(_ context.Context, channelID int64, messageID, _ int, slice rpc.MediaSlice) (rpc.MediaStream, error) {
	f.gotMediaChannel, f.gotMediaMessage = channelID, messageID
	f.gotSlice = slice
	if f.mediaE != nil {
		return rpc.MediaStream{}, f.mediaE
	}
	body := f.mediaBody
	length := int64(len(f.media))
	if body == nil {
		// Honour the requested slice the way the worker does.
		payload := f.media
		offset := int64(0)
		switch {
		case f.mediaSize <= 0:
			length = 0
		case slice.Tail > 0:
			tail := slice.Tail
			if tail > f.mediaSize {
				tail = f.mediaSize
			}
			offset = f.mediaSize - tail
			if offset >= int64(len(payload)) {
				payload = nil
			} else {
				payload = payload[offset:]
			}
		case slice.Offset > 0:
			if slice.Offset >= int64(len(payload)) {
				offset, payload = f.mediaSize, nil
			} else {
				offset, payload = slice.Offset, payload[slice.Offset:]
			}
		}
		if slice.Limit > 0 && f.mediaSize > 0 && int64(len(payload)) > slice.Limit {
			payload = payload[:slice.Limit]
		}
		body = io.NopCloser(bytes.NewReader(payload))
		length = int64(len(payload))
	}
	return rpc.MediaStream{
		MimeType: f.mediaType,
		Filename: f.mediaName,
		Size:     f.mediaSize,
		Offset:   slice.Offset,
		Length:   length,
		Body:     body,
	}, nil
}

func (f *fakeBackend) CustomEmoji(_ context.Context, documentID int64) (rpc.MediaFile, error) {
	f.gotCustomEmoji = documentID
	return f.customEmoji, f.customEmojiE
}

func (f *fakeBackend) Find(_ context.Context, req rpc.FindRequest) (rpc.FindResult, error) {
	f.gotFind, f.gotOp = req, "find"
	return f.find, f.findErr
}

func (f *fakeBackend) Channel(_ context.Context, ref rpc.ChannelRef) (rpc.ChannelResult, error) {
	f.gotRef, f.gotOp = ref, "channel"
	return f.channel, f.chanInfo
}

func (f *fakeBackend) Join(_ context.Context, ref rpc.ChannelRef) (rpc.JoinResult, error) {
	f.gotRef, f.gotOp = ref, "join"
	return f.join, f.joinErr
}

func (f *fakeBackend) Avatar(_ context.Context, ref rpc.ChannelRef) (rpc.AvatarFile, error) {
	f.gotRef, f.gotOp = ref, "avatar"
	return f.avatar, f.avatarE
}

func (f *fakeBackend) Leave(_ context.Context, ref rpc.ChannelRef) (rpc.LeaveResult, error) {
	f.gotRef, f.gotOp = ref, "leave"
	return f.leave, f.leaveE
}

func (f *fakeBackend) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	f.gotSend = req
	return f.send, f.sendE
}

func (f *fakeBackend) DeleteMessage(_ context.Context, req rpc.DeleteMessageRequest) (rpc.DeleteMessageResult, error) {
	f.gotDelete = req
	return f.deleted, f.deleteE
}

func (f *fakeBackend) EditMessage(_ context.Context, req rpc.EditRequest) (rpc.EditResult, error) {
	f.gotEdit = req
	return f.edit, f.editE
}

// pipeListener yields exactly one pre-built connection to http.Server and then
// blocks until Close. The sandbox forbids binding ports, and a net.Pipe carries
// exactly the same HTTP bytes as a socket.
type pipeListener struct {
	c    net.Conn
	used bool
	done chan struct{}
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

func (l *pipeListener) Close() error   { close(l.done); return nil }
func (l *pipeListener) Addr() net.Addr { return l.c.LocalAddr() }

// serveRPC starts an RPC server over an in-memory connection and returns a raw
// HTTP client that reaches it.
func serveRPC(t *testing.T, backend rpc.Backend) *http.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	srv := &http.Server{Handler: rpc.NewHandler(backend)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(newPipeListener(serverConn))
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("rpc server did not stop in time")
		}
	})
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return clientConn, nil
			},
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}
}

// rpcClient builds the typed client over an in-memory connection.
func rpcClient(t *testing.T, backend rpc.Backend, timeout time.Duration) *rpc.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	srv := &http.Server{Handler: rpc.NewHandler(backend)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(newPipeListener(serverConn))
	}()
	if timeout == 0 {
		timeout = time.Second
	}
	client := rpc.NewClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return clientConn, nil
	}, timeout)
	t.Cleanup(func() {
		client.Close()
		_ = srv.Close()
		<-done
	})
	return client
}

func TestHealthRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	backend := &fakeBackend{health: rpc.Health{
		Key: "official", Type: "official", State: rpc.StateConnected,
		Authorized: true, Transport: "ready", RPCReady: true, PID: 4242,
		StartedAt: now, UptimeSeconds: 90, ThisDC: 2,
		LastSuccessAt: &now,
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if got.Key != "official" || got.State != rpc.StateConnected || !got.Authorized {
		t.Fatalf("health = %+v", got)
	}
	if got.PID != 4242 || got.UptimeSeconds != 90 || got.ThisDC != 2 {
		t.Fatalf("health = %+v", got)
	}
	if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(now) {
		t.Fatalf("last success = %v", got.LastSuccessAt)
	}
}

func TestProfileRoundTripAndUnauthorizedState(t *testing.T) {
	backend := &fakeBackend{profile: rpc.Profile{
		Key: "the-cat", Type: "custom", Authorized: false,
		AuthorizationState: rpc.AuthNeedsAuth,
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Profile(context.Background())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if got.Authorized || got.AuthorizationState != rpc.AuthNeedsAuth {
		t.Fatalf("profile = %+v", got)
	}
	if got.User != nil {
		t.Fatalf("an unauthorized profile must not carry a user: %+v", got.User)
	}
}

func TestChannelsPassPaginationAndReturnData(t *testing.T) {
	unread := 4
	backend := &fakeBackend{channels: rpc.ChannelPage{
		Channels: []rpc.Channel{{
			ID: 500, Title: "کانال نمونه", Username: "sample", Kind: "channel",
			HasAvatar: true, UnreadCount: &unread, LastMessageID: 120,
		}},
		Page: rpc.PageInfo{Page: 2, PerPage: 10, Total: 21, HasMore: true, NextPage: 3},
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Channels(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("Channels: %v", err)
	}
	if backend.gotPage != 2 || backend.gotPerPage != 10 {
		t.Fatalf("server saw page=%d per_page=%d", backend.gotPage, backend.gotPerPage)
	}
	if len(got.Channels) != 1 || got.Channels[0].Title != "کانال نمونه" {
		t.Fatalf("channels = %+v", got.Channels)
	}
	if got.Channels[0].UnreadCount == nil || *got.Channels[0].UnreadCount != 4 {
		t.Fatalf("unread = %v", got.Channels[0].UnreadCount)
	}
	if got.Page.Total != 21 || !got.Page.HasMore || got.Page.NextPage != 3 {
		t.Fatalf("page = %+v", got.Page)
	}
}

func TestMessagesRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	backend := &fakeBackend{messages: rpc.MessagePage{
		Channel: rpc.Channel{ID: 500, Title: "کانال نمونه"},
		Messages: []rpc.Message{{
			ID: 1233, Date: at, Text: "سلام",
			Sender:           &rpc.Sender{ID: 42, Name: "علی", Username: "ali"},
			Media:            &rpc.Media{Type: "photo", Label: "عکس"},
			ReplyToMessageID: 1200,
		}},
		Page: rpc.PageInfo{Page: 1, PerPage: 20, Total: 1, NextPage: 2},
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if backend.gotChannel != 500 {
		t.Fatalf("channel = %d", backend.gotChannel)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	m := got.Messages[0]
	if m.ID != 1233 || m.Text != "سلام" || m.ReplyToMessageID != 1200 {
		t.Fatalf("message = %+v", m)
	}
	if m.Sender == nil || m.Sender.Username != "ali" {
		t.Fatalf("sender = %+v", m.Sender)
	}
	if m.Media == nil || m.Media.Type != "photo" {
		t.Fatalf("media = %+v", m.Media)
	}
}

func TestMessagesRoundTripViewsAndReactions(t *testing.T) {
	chosen := true
	views := 0
	backend := &fakeBackend{messages: rpc.MessagePage{
		Messages: []rpc.Message{
			{
				ID: 1, Views: &views,
				Reactions: []rpc.Reaction{
					{Type: "emoji", Emoji: "👍", Count: 12},
					{Type: "emoji", Emoji: "❤️", Count: 8, Chosen: &chosen},
					{Type: "custom_emoji", DocumentID: 5368324170671202286, Count: 2},
				},
			},
			// No views and no reactions: views must stay null, reactions [].
			{ID: 2},
		},
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	first := got.Messages[0]
	if first.Views == nil || *first.Views != 0 {
		t.Fatalf("views = %v, want a real 0", first.Views)
	}
	if first.Reactions[1].Chosen == nil || !*first.Reactions[1].Chosen {
		t.Fatalf("chosen = %v", first.Reactions[1].Chosen)
	}
	if first.Reactions[2].DocumentID != 5368324170671202286 {
		t.Fatalf("custom reaction = %+v", first.Reactions[2])
	}
	second := got.Messages[1]
	if second.Views != nil {
		t.Fatalf("views = %v, want nil", *second.Views)
	}
	if second.Reactions == nil || len(second.Reactions) != 0 {
		t.Fatalf("reactions = %#v, want []", second.Reactions)
	}

	// An omitted views field stays null on the wire: the worker never turns
	// "Telegram did not say" into a count.
	raw, err := json.Marshal(rpc.Message{ID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"views":null`; !strings.Contains(string(raw), want) {
		t.Fatalf("json = %s, want %s", raw, want)
	}
}

func TestClientDecodesANullReactionListAsEmpty(t *testing.T) {
	// The worker always fills the field; the gateway must still never render
	// null if it ever reads a page written by an older worker.
	clientConn, serverConn := net.Pipe()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"channel":{"id":500},"messages":[{"id":1,"reactions":null}],"page":{"page":1}}`)
	})}
	go func() { _ = srv.Serve(newPipeListener(serverConn)) }()
	client := rpc.NewClientWithDialer(func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}, time.Second)
	page, err := client.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := page.Messages[0].Reactions; got == nil || len(got) != 0 {
		t.Fatalf("reactions = %#v, want []", got)
	}
}

func TestCustomEmojiRoundTrip(t *testing.T) {
	backend := &fakeBackend{customEmoji: rpc.MediaFile{
		MimeType: "application/x-tgsticker",
		Data:     []byte("emoji-bytes"),
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.CustomEmoji(context.Background(), 5368324170671202286)
	if err != nil {
		t.Fatalf("CustomEmoji: %v", err)
	}
	if backend.gotCustomEmoji != 5368324170671202286 {
		t.Fatalf("document id = %d", backend.gotCustomEmoji)
	}
	if string(got.Data) != "emoji-bytes" || got.MimeType != "application/x-tgsticker" {
		t.Fatalf("file = %+v", got)
	}

	backend.customEmojiE = rpc.NewError(rpc.CodeNotFound, "no such emoji", 0)
	if _, err := client.CustomEmoji(context.Background(), 1); err == nil {
		t.Fatal("a missing emoji must be an error")
	}
}

func TestServerRejectsInvalidPagination(t *testing.T) {
	for _, q := range []string{
		"page=0", "page=abc", "page=-1",
		"per_page=0", "per_page=101", "per_page=abc",
	} {
		t.Run(q, func(t *testing.T) {
			backend := &fakeBackend{}
			httpc := serveRPC(t, backend)
			resp, err := httpc.Get("http://worker/channels?" + q)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d for %q", resp.StatusCode, q)
			}
			var body rpc.ErrorBody
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != rpc.CodeInvalidInput {
				t.Fatalf("code = %q", body.Error.Code)
			}
			if backend.calls != 0 {
				t.Fatal("an invalid request must never reach the backend")
			}
		})
	}
}

func TestServerDefaultsPagination(t *testing.T) {
	backend := &fakeBackend{}
	client := rpcClient(t, backend, 0)

	if _, err := client.Channels(context.Background(), 0, 0); err != nil {
		t.Fatalf("Channels: %v", err)
	}
	if backend.gotPage != 1 || backend.gotPerPage != 20 {
		t.Fatalf("defaults = page %d per_page %d", backend.gotPage, backend.gotPerPage)
	}
}

func TestServerMapsBackendErrorsToCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
		http int
	}{
		{"needs auth", rpc.ErrNeedsAuth, rpc.CodeNeedsAuth, http.StatusConflict},
		{"not connected", rpc.ErrNotConnected, rpc.CodeNotConnected, http.StatusServiceUnavailable},
		{"channel not found", rpc.ErrChannelNotFound, rpc.CodeChannelNotFound, http.StatusNotFound},
		{"coded", rpc.NewError(rpc.CodeRateLimited, "slow down", 7), rpc.CodeRateLimited, http.StatusTooManyRequests},
		{"internal", errors.New("boom"), rpc.CodeInternal, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{chanErr: tc.err}
			httpc := serveRPC(t, backend)

			resp, err := httpc.Get("http://worker/channels?page=1&per_page=5")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.http {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.http)
			}
			var body rpc.ErrorBody
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.code)
			}
			if strings.Contains(body.Error.Message, "boom") {
				t.Fatalf("the internal error leaked into the response: %q", body.Error.Message)
			}
		})
	}
}

func TestClientReturnsTypedErrors(t *testing.T) {
	backend := &fakeBackend{msgErr: rpc.NewError(rpc.CodeRateLimited, "slow down", 7)}
	client := rpcClient(t, backend, 0)

	_, err := client.Messages(context.Background(), 1, 1, 20)
	if err == nil {
		t.Fatal("expected an error")
	}
	var coded *rpc.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error is not a *rpc.Error: %v", err)
	}
	if coded.Code != rpc.CodeRateLimited || coded.RetryAfter != 7 {
		t.Fatalf("error = %+v", coded)
	}
}

func TestClientMapsSentinelErrors(t *testing.T) {
	backend := &fakeBackend{profileE: rpc.ErrNeedsAuth}
	client := rpcClient(t, backend, 0)

	_, err := client.Profile(context.Background())
	if !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}
}

func TestClientTimesOutOnASlowBackend(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			_, _ = w.Write([]byte(`{"key":"a"}`))
		}
	})
	clientConn, serverConn := net.Pipe()
	srv := &http.Server{Handler: slow}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(newPipeListener(serverConn))
	}()
	client := rpc.NewClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return clientConn, nil
	}, 100*time.Millisecond)
	defer func() {
		client.Close()
		_ = srv.Close()
		<-done
	}()

	_, err := client.Health(context.Background())
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestClientReportsUnreachableWorker(t *testing.T) {
	client := rpc.NewClient("/nonexistent/telesync-test.sock", 100*time.Millisecond)
	defer client.Close()
	_, err := client.Health(context.Background())
	if !errors.Is(err, rpc.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestClientRejectsMalformedResponses(t *testing.T) {
	bad := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"key":`)
	})
	clientConn, serverConn := net.Pipe()
	srv := &http.Server{Handler: bad}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(newPipeListener(serverConn))
	}()
	client := rpc.NewClientWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return clientConn, nil
	}, time.Second)
	defer func() {
		client.Close()
		_ = srv.Close()
		<-done
	}()

	if _, err := client.Health(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestServerRejectsUnknownRoutesAndMethods(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/nope", http.StatusNotFound},
		{http.MethodPost, "/channels", http.StatusMethodNotAllowed},
		{http.MethodGet, "/channels/abc/messages", http.StatusBadRequest},
		{http.MethodGet, "/channels/0/messages", http.StatusBadRequest},
		{http.MethodGet, "/channels/5/extra", http.StatusNotFound},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			httpc := serveRPC(t, &fakeBackend{})
			req, err := http.NewRequest(tc.method, "http://worker"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := httpc.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestServerRejectsUnknownQueryParameters(t *testing.T) {
	httpc := serveRPC(t, &fakeBackend{})
	resp, err := httpc.Get("http://worker/channels?page=1&perpag=20")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestErrorStringDoesNotLeakInternals(t *testing.T) {
	err := rpc.NewError(rpc.CodeInternal, "internal error", 0)
	if err.Error() == "" {
		t.Fatal("an error must have a message")
	}
}

func TestMediaRoundTrip(t *testing.T) {
	backend := &fakeBackend{messages: rpc.MessagePage{
		Channel: rpc.Channel{ID: 500, Title: "chan"},
		Messages: []rpc.Message{{
			ID:   179,
			Text: "x",
			Media: &rpc.Media{
				ID: "500:9", Type: "photo", Label: "عکس",
				MimeType: "image/jpeg", Size: 98765,
			},
		}},
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	m := got.Messages[0].Media
	if m == nil {
		t.Fatal("media missing")
	}
	if m.ID != "500:9" || m.Type != "photo" || m.MimeType != "image/jpeg" || m.Size != 98765 {
		t.Fatalf("media = %+v", m)
	}
}

func TestMediaDownloadRoundTrip(t *testing.T) {
	payload := []byte("jpeg-bytes")
	backend := &fakeBackend{
		media: payload, mediaType: "image/jpeg", mediaName: "photo.jpg", mediaSize: int64(len(payload)),
	}
	client := rpcClient(t, backend, 0)

	got, err := client.Media(context.Background(), 500, 179, 0, rpc.MediaSlice{})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if body := readMedia(t, got); body != string(payload) {
		t.Fatalf("body = %q, want %q", body, payload)
	}
	if got.MimeType != "image/jpeg" {
		t.Fatalf("mime = %q", got.MimeType)
	}
	if got.Filename != "photo.jpg" {
		t.Fatalf("filename = %q, want %q", got.Filename, "photo.jpg")
	}
	if got.Size != int64(len(payload)) || got.Length != int64(len(payload)) {
		t.Fatalf("size = %d length = %d", got.Size, got.Length)
	}
	if backend.gotMediaChannel != 500 || backend.gotMediaMessage != 179 {
		t.Fatalf("server saw channel=%d message=%d", backend.gotMediaChannel, backend.gotMediaMessage)
	}
}

func TestMediaDownloadCarriesTheSliceThrough(t *testing.T) {
	payload := []byte("0123456789")
	backend := &fakeBackend{
		media: payload, mediaType: "audio/mpeg", mediaSize: int64(len(payload)),
	}
	client := rpcClient(t, backend, 0)

	got, err := client.Media(context.Background(), 500, 179, 0, rpc.MediaSlice{Offset: 3, Limit: 4})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if body := readMedia(t, got); body != "3456" {
		t.Fatalf("body = %q, want %q", body, "3456")
	}
	if backend.gotSlice != (rpc.MediaSlice{Offset: 3, Limit: 4}) {
		t.Fatalf("backend saw slice %+v, want offset 3 limit 4", backend.gotSlice)
	}
	if got.Size != 10 || got.Length != 4 {
		t.Fatalf("size = %d length = %d, want 10/4", got.Size, got.Length)
	}
}

func TestMediaDownloadHasNoFilenameWhenThereIsNone(t *testing.T) {
	backend := &fakeBackend{media: []byte("jpeg"), mediaType: "image/jpeg", mediaSize: 4}
	client := rpcClient(t, backend, 0)

	got, err := client.Media(context.Background(), 500, 179, 0, rpc.MediaSlice{})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if got.Filename != "" {
		t.Fatalf("filename = %q, want none: nothing may invent one", got.Filename)
	}
	readMedia(t, got)
}

// streamChunkSize forces the server's response buffer to flush: net/http holds
// small writes back, and a real attachment chunk is far larger than that.
const streamChunkSize = 8 << 10

// blockingBody hands out its first chunk and then blocks. It stands for a large
// document that is still arriving from Telegram when the caller starts reading.
type blockingBody struct {
	first   []byte
	given   bool
	release chan struct{}
}

func (b *blockingBody) Read(p []byte) (int, error) {
	if !b.given {
		b.given = true
		return copy(p, b.first), nil
	}
	<-b.release
	return 0, io.EOF
}

func (b *blockingBody) Close() error { return nil }

// firstChunk is one chunk of a document: a marker at the front and enough
// padding to reach the wire rather than sitting in a buffer.
func firstChunk() []byte {
	chunk := make([]byte, streamChunkSize)
	copy(chunk, "first")
	return chunk
}

// TestMediaReturnsBeforeTheAttachmentIsComplete is what makes a 60 MB document
// possible: the caller gets control of the answer while the bytes are still
// arriving, instead of waiting for the whole file to be collected first.
func TestMediaReturnsBeforeTheAttachmentIsComplete(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	// Whatever happens below, the worker is never left waiting on this test.
	defer unblock()

	backend := &fakeBackend{
		mediaType: "audio/mpeg",
		mediaSize: 1 << 20,
		mediaBody: &blockingBody{first: firstChunk(), release: release},
	}
	client := rpcClient(t, backend, 0)

	type answer struct {
		stream rpc.MediaStream
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		stream, err := client.Media(context.Background(), 500, 179, 0, rpc.MediaSlice{})
		done <- answer{stream, err}
	}()

	var stream rpc.MediaStream
	select {
	case a := <-done:
		if a.err != nil {
			t.Fatalf("Media: %v", a.err)
		}
		stream = a.stream
	case <-time.After(5 * time.Second):
		t.Fatal("Media waited for the whole attachment to arrive; nothing can be streamed")
	}

	buf := make([]byte, 5)
	_, readErr := io.ReadFull(stream.Body, buf)
	// The writer is released before the body is closed: closing a half-read
	// HTTP body waits for the writer to finish.
	unblock()
	_ = stream.Body.Close()

	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}
	if string(buf) != "first" {
		t.Fatalf("first bytes = %q, want %q", buf, "first")
	}
}

func readMedia(t *testing.T, stream rpc.MediaStream) string {
	t.Helper()
	defer stream.Body.Close()
	data, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatalf("read media: %v", err)
	}
	return string(data)
}

func TestMediaRouteRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/channels/0/messages/1/media", http.StatusBadRequest},
		{"/channels/500/messages/0/media", http.StatusBadRequest},
		{"/channels/abc/messages/1/media", http.StatusBadRequest},
		{"/channels/500/messages/1/media/extra", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			httpc := serveRPC(t, &fakeBackend{})
			resp, err := httpc.Get("http://worker" + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("%s = %d, want %d", tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestSingleMessageRoundTrip(t *testing.T) {
	backend := &fakeBackend{message: rpc.MessagePage{
		Channel:  rpc.Channel{ID: 500, Title: "chan"},
		Messages: []rpc.Message{{ID: 179, Text: "hello", ReplyToMessageID: 100}},
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Message(context.Background(), 500, 179)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Channel.ID != 500 || len(got.Messages) != 1 {
		t.Fatalf("page = %+v", got)
	}
	if got.Messages[0].ID != 179 || got.Messages[0].ReplyToMessageID != 100 {
		t.Fatalf("message = %+v", got.Messages[0])
	}
	if backend.gotMessageChannel != 500 || backend.gotMessageID != 179 {
		t.Fatalf("server saw channel=%d message=%d", backend.gotMessageChannel, backend.gotMessageID)
	}
}

func TestSingleMessageRouteRejectsBadIDs(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/channels/0/messages/1", http.StatusBadRequest},
		{"/channels/500/messages/0", http.StatusBadRequest},
		{"/channels/500/messages/abc", http.StatusBadRequest},
	} {
		t.Run(tc.path, func(t *testing.T) {
			httpc := serveRPC(t, &fakeBackend{})
			resp, err := httpc.Get("http://worker" + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("%s = %d, want %d", tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestSingleMessageMissingIsNotFound(t *testing.T) {
	backend := &fakeBackend{messageE: rpc.NewError(rpc.CodeNotFound, "no such message", 0)}
	httpc := serveRPC(t, backend)
	resp, err := httpc.Get("http://worker/channels/500/messages/179")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestFindRoundTrip(t *testing.T) {
	backend := &fakeBackend{find: rpc.FindResult{Channels: []rpc.ChannelInfo{{
		ID: "500", Type: "channel", Title: "Example", Username: "example",
		IsMember: true, MembershipStatus: "member",
		Permissions: rpc.Permissions{Read: true},
	}}}}
	client := rpcClient(t, backend, 0)

	got, err := client.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByUsername, Username: "example"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got.Channels) != 1 || got.Channels[0].ID != "500" {
		t.Fatalf("channels = %+v", got.Channels)
	}
	if !got.Channels[0].Permissions.Read {
		t.Fatalf("permissions = %+v", got.Channels[0].Permissions)
	}
	if backend.gotFind.Kind != rpc.FindByUsername || backend.gotFind.Username != "example" {
		t.Fatalf("server saw %+v", backend.gotFind)
	}
}

func TestFindRejectsAGetRequest(t *testing.T) {
	httpc := serveRPC(t, &fakeBackend{})
	resp, err := httpc.Get("http://worker/find?kind=text&text=x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestChannelAndJoinRoundTrip(t *testing.T) {
	backend := &fakeBackend{
		channel: rpc.ChannelResult{Channel: rpc.ChannelInfo{ID: "500", Title: "Example"}},
		join:    rpc.JoinResult{Channel: rpc.ChannelInfo{ID: "500"}, Outcome: rpc.OutcomeJoined},
	}
	client := rpcClient(t, backend, 0)

	info, err := client.Channel(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Channel: %v", err)
	}
	if info.Channel.ID != "500" || backend.gotRef.ChannelID != 500 {
		t.Fatalf("channel = %+v ref = %+v", info, backend.gotRef)
	}

	joined, err := client.Join(context.Background(), rpc.ChannelRef{Invite: "AbCdEf123456"})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if joined.Outcome != rpc.OutcomeJoined {
		t.Fatalf("outcome = %q", joined.Outcome)
	}
	if backend.gotRef.Invite != "AbCdEf123456" {
		t.Fatalf("server saw ref %+v", backend.gotRef)
	}
}

func TestAvatarRoundTripCarriesBytes(t *testing.T) {
	backend := &fakeBackend{avatar: rpc.AvatarFile{
		MimeType: "image/jpeg", PhotoID: 9, Data: []byte("jpeg"),
	}}
	client := rpcClient(t, backend, 0)

	got, err := client.Avatar(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Avatar: %v", err)
	}
	if string(got.Data) != "jpeg" || got.MimeType != "image/jpeg" || got.PhotoID != 9 {
		t.Fatalf("avatar = %+v", got)
	}
}

func TestWorkerErrorsKeepTheirMeaningOnTheWire(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		expect error
	}{
		{"invite invalid", rpc.NewError(rpc.CodeInviteInvalid, "expired", 0),
			http.StatusBadRequest, rpc.ErrInviteInvalid},
		{"peer unresolved", rpc.NewError(rpc.CodePeerUnresolved, "no hash", 0),
			http.StatusBadRequest, rpc.ErrPeerUnresolved},
		{"no avatar", rpc.NewError(rpc.CodeNoAvatar, "none", 0),
			http.StatusNotFound, rpc.ErrNoAvatar},
		{"membership limit", rpc.NewError(rpc.CodeMembershipLimit, "too many", 0),
			http.StatusConflict, rpc.ErrMembershipLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{joinErr: tc.err}
			client := rpcClient(t, backend, 0)
			_, err := client.Join(context.Background(), rpc.ChannelRef{ChannelID: 500})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, tc.expect) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.expect)
			}
		})
	}
}

func TestMediaStreamReadAllRejectsABodyShorterThanItPromised(t *testing.T) {
	// A caller that turns a stream into one buffer is about to send it on — to
	// Telegram, to WordPress. A body that ends before the length it announced is
	// a truncated attachment, and handing it over publishes a corrupt file as if
	// it were whole.
	stream := rpc.MediaStream{Length: 10, Body: io.NopCloser(strings.NewReader("0123"))}

	data, err := stream.ReadAll()
	if err == nil {
		t.Fatalf("ReadAll accepted %d of the 10 bytes it promised as whole: %q", len(data), data)
	}
	if !strings.Contains(err.Error(), "4 of 10 bytes") {
		t.Fatalf("err = %q, want it to say 4 of 10 bytes", err)
	}
}

func TestMediaStreamReadAllAcceptsAWholeBody(t *testing.T) {
	stream := rpc.MediaStream{Length: 10, Body: io.NopCloser(strings.NewReader("0123456789"))}

	data, err := stream.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "0123456789" {
		t.Fatalf("data = %q, want %q", data, "0123456789")
	}
}

func TestMediaStreamReadAllAcceptsAnUnknownLength(t *testing.T) {
	// With no reported size nothing was promised, and Telegram stopping is the
	// only way to learn the attachment is over. That must stay a clean read.
	stream := rpc.MediaStream{Body: io.NopCloser(strings.NewReader("0123"))}

	data, err := stream.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "0123" {
		t.Fatalf("data = %q, want %q", data, "0123")
	}
}
