package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/worker"
)

// fakeTelegram stands in for gotdclient.Manager.
type fakeTelegram struct {
	mu       sync.Mutex
	status   tgapi.Status
	dialogs  []tgapi.Channel
	dialErr  error
	hist     tgapi.HistoryPage
	histErr  error
	readCall int
	// historyCalls records the (beforeID, limit) pairs requested.
	historyCalls      [][2]int
	mediaBytes        []byte
	mediaName         string
	mediaType         string
	mediaSize         int64
	mediaErr          error
	oneMessage        tgapi.Message
	oneMessageErr     error
	oneMessageChannel tgapi.Channel
	oneMessageID      int
	oneMessageCalls   int

	findResult  []tgapi.ChannelInfo
	findErr     error
	gotQuery    tgapi.ChannelQuery
	resolved    tgapi.ChannelInfo
	resolveErr  error
	gotRef      tgapi.ChannelRef
	joinResult  tgapi.JoinResult
	joinErr     error
	avatar      tgapi.AvatarFile
	avatarErr   error
	findCalls   int
	joinCalls   int
	avatarCalls int
	leaves      []int64
	leaveErr    error

	emoji    tgapi.EmojiFile
	emojiErr error
	gotEmoji int64

	sentRef         tgapi.ChannelRef
	sentReq         tgapi.SendRequest
	sentResult      tgapi.SendResult
	sentErr         error
	albumReq        tgapi.SendAlbumRequest
	albumResult     tgapi.SendAlbumResult
	albumErr        error
	deleteChannel   tgapi.Channel
	deleteMessageID int
	deleteCalls     int
	deleteErr       error
}

func (f *fakeTelegram) Start(context.Context) {}

func (f *fakeTelegram) Status(context.Context) tgapi.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeTelegram) Dialogs(context.Context) ([]tgapi.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dialogs, f.dialErr
}

func (f *fakeTelegram) History(_ context.Context, _ tgapi.Channel, beforeID, limit int) (tgapi.HistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyCalls = append(f.historyCalls, [2]int{beforeID, limit})
	return f.hist, f.histErr
}

func (f *fakeTelegram) FindChannels(_ context.Context, q tgapi.ChannelQuery) ([]tgapi.ChannelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.findCalls++
	f.gotQuery = q
	return f.findResult, f.findErr
}

func (f *fakeTelegram) ResolveChannel(_ context.Context, ref tgapi.ChannelRef) (tgapi.ChannelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotRef = ref
	return f.resolved, f.resolveErr
}

func (f *fakeTelegram) JoinChannel(_ context.Context, ref tgapi.ChannelRef) (tgapi.JoinResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joinCalls++
	f.gotRef = ref
	return f.joinResult, f.joinErr
}

func (f *fakeTelegram) DownloadCustomEmoji(_ context.Context, documentID int64) (tgapi.EmojiFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotEmoji = documentID
	return f.emoji, f.emojiErr
}

func (f *fakeTelegram) DownloadChannelAvatar(_ context.Context, ref tgapi.ChannelRef) (tgapi.AvatarFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.avatarCalls++
	f.gotRef = ref
	return f.avatar, f.avatarErr
}

func (f *fakeTelegram) SendMessage(_ context.Context, ref tgapi.ChannelRef, req tgapi.SendRequest) (tgapi.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentRef, f.sentReq = ref, req
	return f.sentResult, f.sentErr
}

func (f *fakeTelegram) SendAlbum(_ context.Context, _ tgapi.ChannelRef, req tgapi.SendAlbumRequest) (tgapi.SendAlbumResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.albumReq = req
	return f.albumResult, f.albumErr
}

func (f *fakeTelegram) EditMessage(context.Context, tgapi.ChannelRef, tgapi.EditRequest) (tgapi.EditResult, error) {
	return tgapi.EditResult{}, nil
}

func (f *fakeTelegram) DeleteMessage(_ context.Context, channel tgapi.Channel, messageID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteChannel, f.deleteMessageID = channel, messageID
	f.deleteCalls++
	return f.deleteErr
}

func (f *fakeTelegram) LeaveChannel(_ context.Context, ref tgapi.ChannelRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaves = append(f.leaves, ref.ID)
	return f.leaveErr
}

func startedAt() time.Time {
	return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
}

func newService(t *testing.T, tg *fakeTelegram, opts ...worker.Option) *worker.Service {
	t.Helper()
	cfg := appconfig.Client{
		Key:  "official",
		Type: appconfig.Official,
	}
	s := worker.NewService(cfg, tg, worker.Options{
		Now: func() time.Time { return startedAt().Add(90 * time.Second) },
		PID: 4242,
	}, opts...)
	return s
}

func TestHealthSeparatesProcessFromConnectionAndAuth(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateConnecting, ThisDC: 2,
	}}
	s := newService(t, tg)

	h := s.Health(context.Background())
	if h.Key != "official" || h.Type != "official" {
		t.Fatalf("identity = %+v", h)
	}
	if h.PID != 4242 {
		t.Errorf("pid = %d", h.PID)
	}
	if h.State != rpc.StateStarting {
		t.Errorf("state = %q, want starting while the transport is still connecting", h.State)
	}
	if h.Authorized {
		t.Error("a connecting client must not claim authorization")
	}
	if h.Transport != "connecting" {
		t.Errorf("transport = %q", h.Transport)
	}
	if !h.RPCReady {
		t.Error("health answered over RPC, so rpc_ready must be true")
	}
}

func TestHealthReportsConnectedButUnauthorized(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized, ThisDC: 2}}
	s := newService(t, tg)
	h := s.Health(context.Background())
	if h.State != rpc.StateNeedsAuth {
		t.Fatalf("state = %q, want needs_auth", h.State)
	}
	if h.Authorized {
		t.Error("an unauthorized session must not report authorized")
	}
	if h.Transport != "ready" {
		t.Errorf("transport = %q; the transport is up even though the session is not", h.Transport)
	}
}

func TestHealthReportsReadyAndUptime(t *testing.T) {
	at := time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)
	success := at.Add(time.Hour)
	tg := &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2,
		User:        &tgapi.Account{ID: 7, Username: "me"},
		LastSuccess: success,
	}}
	s := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg,
		worker.Options{
			PID:       1,
			StartedAt: at,
			Now:       func() time.Time { return at.Add(2 * time.Hour) },
		})

	h := s.Health(context.Background())
	if h.State != rpc.StateConnected || !h.Authorized {
		t.Fatalf("health = %+v", h)
	}
	if h.UptimeSeconds < 7100 || h.UptimeSeconds > 7300 {
		t.Errorf("uptime = %d", h.UptimeSeconds)
	}
	if h.LastSuccessAt == nil || !h.LastSuccessAt.Equal(success) {
		t.Errorf("last success = %v, want %v", h.LastSuccessAt, success)
	}
}

func TestHealthReportsWhetherASessionFileExists(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "official.json")

	s := worker.NewService(appconfig.Client{
		Key: "official", Type: appconfig.Official, SessionPath: sessionPath,
	}, tg, worker.Options{})

	if h := s.Health(context.Background()); h.SessionPresent {
		t.Fatal("a missing session file must report session_present=false")
	}
	if err := os.WriteFile(sessionPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h := s.Health(context.Background()); !h.SessionPresent {
		t.Fatal("an existing session file must report session_present=true")
	}
}

func TestHealthDoesNotInventALastSuccess(t *testing.T) {
	// Connected transport, but no probe has ever succeeded: the adapter says
	// so, and the worker must not substitute the current time.
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateReady, ThisDC: 2}}
	s := newService(t, tg)
	h := s.Health(context.Background())
	if h.LastSuccessAt != nil {
		t.Fatalf("last success = %v, want nil", h.LastSuccessAt)
	}
}

func TestHealthCarriesASanitizedError(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateError,
		Err:   "اتصال به تلگرام برقرار نیست.",
	}}
	s := newService(t, tg)
	h := s.Health(context.Background())
	if h.State != rpc.StateError {
		t.Fatalf("state = %q", h.State)
	}
	if h.LastError == "" {
		t.Error("an error state must explain itself")
	}
}

func TestProfileIsExplicitWhenUnauthorized(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized, ThisDC: 2}}
	s := newService(t, tg)

	p, err := s.Profile(context.Background())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if p.Authorized || p.AuthorizationState != rpc.AuthNeedsAuth {
		t.Fatalf("profile = %+v", p)
	}
	if p.User != nil {
		t.Fatalf("an unauthorized profile must not fabricate a user: %+v", p.User)
	}
	if p.Key != "official" {
		t.Errorf("key = %q", p.Key)
	}
}

func TestProfileReturnsTheAccountWhenAuthorized(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2,
		User: &tgapi.Account{ID: 7, Username: "me", FirstName: "من", LastName: "X"},
	}}
	s := newService(t, tg)
	p, err := s.Profile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !p.Authorized || p.AuthorizationState != rpc.AuthAuthorized {
		t.Fatalf("profile = %+v", p)
	}
	if p.User == nil || p.User.ID != 7 || p.User.Username != "me" {
		t.Fatalf("user = %+v", p.User)
	}
}

func TestChannelsPageThroughTheSnapshot(t *testing.T) {
	var dialogs []tgapi.Channel
	for i := 1; i <= 25; i++ {
		dialogs = append(dialogs, tgapi.Channel{ID: int64(100 + i), Title: "ch"})
	}
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: dialogs,
	}
	s := newService(t, tg)

	page, err := s.Channels(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("Channels: %v", err)
	}
	if len(page.Channels) != 10 {
		t.Fatalf("channels = %d", len(page.Channels))
	}
	if page.Channels[0].ID != 111 {
		t.Errorf("first id = %d, want dialog 11 (page 2 of 10)", page.Channels[0].ID)
	}
	if page.Page.Page != 2 || page.Page.PerPage != 10 || page.Page.Total != 25 {
		t.Errorf("page info = %+v", page.Page)
	}
	if !page.Page.HasMore || page.Page.NextPage != 3 {
		t.Errorf("page info = %+v", page.Page)
	}

	last, err := s.Channels(context.Background(), 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Channels) != 5 || last.Page.HasMore || last.Page.NextPage != 0 {
		t.Errorf("last page = %+v (%d channels)", last.Page, len(last.Channels))
	}

	beyond, err := s.Channels(context.Background(), 99, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(beyond.Channels) != 0 || beyond.Page.Total != 25 {
		t.Errorf("beyond-the-end page = %+v (%d)", beyond.Page, len(beyond.Channels))
	}
}

func TestChannelsCachesTheSnapshotBriefly(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 1, Title: "a"}},
	}
	now := startedAt()
	var mu sync.Mutex
	current := now
	s := worker.NewService(appconfig.Client{Key: "official", Type: appconfig.Official}, tg,
		worker.Options{
			PID:      1,
			Now:      func() time.Time { mu.Lock(); defer mu.Unlock(); return current },
			CacheTTL: time.Minute,
		})

	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	tg.mu.Lock()
	tg.dialogs = []tgapi.Channel{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}}
	tg.mu.Unlock()

	// Within the TTL the snapshot is reused, so page size stays consistent.
	page, err := s.Channels(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 1 {
		t.Fatalf("the snapshot was not reused: total=%d", page.Page.Total)
	}

	mu.Lock()
	current = now.Add(2 * time.Minute)
	mu.Unlock()
	page, err = s.Channels(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 2 {
		t.Fatalf("an expired snapshot must be refreshed: total=%d", page.Page.Total)
	}
}

func TestChannelsRequiresAuthorization(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}
	s := newService(t, tg)
	_, err := s.Channels(context.Background(), 1, 10)
	if !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}

	tg.status = tgapi.Status{State: tgapi.StateConnecting}
	_, err = s.Channels(context.Background(), 1, 10)
	if !errors.Is(err, rpc.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}

func TestChannelsWrapUpstreamErrors(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialErr: tgapi.FloodWait{Seconds: 30, Op: "getDialogs"},
	}
	s := newService(t, tg)
	_, err := s.Channels(context.Background(), 1, 10)
	if err == nil {
		t.Fatal("expected an error")
	}
	var flood tgapi.FloodWait
	if !errors.As(err, &flood) || flood.Seconds != 30 {
		t.Fatalf("err = %v", err)
	}
}

func TestChannelsSurfaceAFloodWaitAsRateLimited(t *testing.T) {
	// Telegram's FLOOD_WAIT must reach the gateway as a rate limit with a retry
	// delay, not as an opaque worker failure. The classification happens on the
	// wire, so this goes through the real handler.
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialErr: tgapi.FloodWait{Seconds: 42, Op: "getDialogs"},
	}
	svc := newService(t, tg)
	handler := rpc.NewHandler(svc)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/channels?page=1&per_page=10", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Logf("content type = %q", got)
	}
	var body rpc.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error == nil || body.Error.Code != rpc.CodeRateLimited || body.Error.RetryAfter != 42 {
		t.Fatalf("body = %+v", body.Error)
	}
}

func TestMessagesPageOlderHistory(t *testing.T) {
	tg := &fakeTelegram{
		status: tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{
			{ID: 500, Title: "chan", Username: "sample", Kind: "channel"},
		},
		hist: tgapi.HistoryPage{
			Messages: []tgapi.Message{
				{ID: 120, Date: time.Unix(1700000000, 0).UTC(), Text: "سلام"},
				{ID: 119, Date: time.Unix(1699990000, 0).UTC(), Text: "hi"},
			},
			HasMore: true,
		},
	}
	s := newService(t, tg)

	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}

	first, err := s.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(first.Messages) != 2 || first.Messages[0].ID != 120 {
		t.Fatalf("messages = %+v", first.Messages)
	}
	if first.Channel.ID != 500 || first.Channel.Title != "chan" {
		t.Fatalf("channel = %+v", first.Channel)
	}
	if first.Page.Page != 1 || !first.Page.HasMore || first.Page.NextPage != 2 {
		t.Fatalf("page = %+v", first.Page)
	}

	// The next page continues strictly below the oldest message already served.
	second, err := s.Messages(context.Background(), 500, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if second.Page.Page != 2 {
		t.Fatalf("page = %+v", second.Page)
	}
	tg.mu.Lock()
	calls := append([][2]int(nil), tg.historyCalls...)
	tg.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("history calls = %v", calls)
	}
	if calls[0][0] != 0 {
		t.Errorf("the first page must request the newest messages, got before_id=%d", calls[0][0])
	}
	if calls[1][0] != 119 {
		t.Errorf("the second page must continue below message 119, got before_id=%d", calls[1][0])
	}
}

func TestMessagesNeverMarkAsRead(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan"}},
		hist:    tgapi.HistoryPage{Messages: []tgapi.Message{{ID: 1}}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Messages(context.Background(), 500, 1, 10); err != nil {
		t.Fatal(err)
	}
	tg.mu.Lock()
	defer tg.mu.Unlock()
	if tg.readCall != 0 {
		t.Fatalf("reading history marked the channel read (%d calls)", tg.readCall)
	}
}

func TestMessagesRejectUnknownChannel(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan"}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	_, err := s.Messages(context.Background(), 999, 1, 10)
	if !errors.Is(err, rpc.ErrChannelNotFound) {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
	tg.mu.Lock()
	calls := len(tg.historyCalls)
	tg.mu.Unlock()
	if calls != 0 {
		t.Error("an unknown channel must not reach Telegram")
	}
}

func TestMessagesResolveAnUncachedChannelWithoutTheSnapshot(t *testing.T) {
	tg := &fakeTelegram{
		status: tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		// The snapshot is empty, as it is right after a restart.
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{{ID: 5}}},
	}
	s := newService(t, tg)
	_, err := s.Messages(context.Background(), 500, 1, 10)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	tg.mu.Lock()
	calls := len(tg.historyCalls)
	tg.mu.Unlock()
	if calls != 1 {
		t.Fatalf("history calls = %d; an uncached channel should still be readable", calls)
	}
}

func TestMessagesRejectsACursorFromAnotherChannel(t *testing.T) {
	tg := &fakeTelegram{
		status: tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{
			{ID: 1, Title: "one"}, {ID: 2, Title: "two"},
		},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{{ID: 50}}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Messages(context.Background(), 1, 1, 10); err != nil {
		t.Fatal(err)
	}
	// Channel 2 has never been paged; asking for page 2 must not reuse
	// channel 1's cursor.
	tg.mu.Lock()
	tg.historyCalls = nil
	tg.mu.Unlock()
	if _, err := s.Messages(context.Background(), 2, 2, 10); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	tg.mu.Lock()
	calls := append([][2]int(nil), tg.historyCalls...)
	tg.mu.Unlock()
	if len(calls) != 1 || calls[0][0] != 0 {
		t.Fatalf("calls = %v; a fresh channel must start from the newest page", calls)
	}
}

func TestPaginationCacheIsBounded(t *testing.T) {
	var dialogs []tgapi.Channel
	for i := 1; i <= worker.MaxCachedChannels+20; i++ {
		dialogs = append(dialogs, tgapi.Channel{ID: int64(i), Title: "c"})
	}
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: dialogs,
		hist:    tgapi.HistoryPage{Messages: []tgapi.Message{{ID: 10}}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 100); err != nil {
		t.Fatal(err)
	}
	// Page 2 holds the rest.
	page, err := s.Channels(context.Background(), 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range page.Channels {
		if _, err := s.Messages(context.Background(), ch.ID, 1, 10); err != nil {
			t.Fatalf("Messages(%d): %v", ch.ID, err)
		}
	}
	if got := s.CachedChannels(); got > worker.MaxCachedChannels {
		t.Fatalf("cached channels = %d, limit %d", got, worker.MaxCachedChannels)
	}
}

func TestChannelDTOOmitsUnavailableUnreadCount(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 1, Title: "a"}},
	}
	s := newService(t, tg)
	page, err := s.Channels(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Channels[0].UnreadCount != nil {
		t.Fatalf("unread = %v, want nil (Telegram reported nothing)", page.Channels[0].UnreadCount)
	}
}

// --- media ---

func (f *fakeTelegram) DownloadMedia(_ context.Context, _ tgapi.Channel, _ int, _ tgapi.MediaSlice) (tgapi.MediaStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mediaErr != nil {
		return tgapi.MediaStream{}, f.mediaErr
	}
	return tgapi.MediaStream{
		MimeType: f.mediaType,
		Filename: f.mediaName,
		Size:     f.mediaSize,
		Length:   int64(len(f.mediaBytes)),
		Body:     io.NopCloser(bytes.NewReader(f.mediaBytes)),
	}, nil
}

func TestMessagesCarryAMediaDescriptorWithAnID(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan", Username: "sample"}},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{
			{
				ID:        179,
				Text:      "hello",
				GroupedID: "9876543210123456789",
				Media: &tgapi.Media{
					Type: "video", Label: "ویدیو",
					MimeType: "video/mp4", Size: 1200, Duration: 12,
					Width: 1280, Height: 720, SupportsStreaming: true,
				},
			},
		}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	page, err := s.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	m := page.Messages[0]
	if m.Media == nil {
		t.Fatal("media missing")
	}
	if m.Media.Type != "video" || m.Media.MimeType != "video/mp4" || m.Media.Size != 1200 ||
		m.Media.Duration != 12 || m.Media.Width != 1280 || m.Media.Height != 720 || !m.Media.SupportsStreaming {
		t.Fatalf("media = %+v", m.Media)
	}
	if m.GroupedID != "9876543210123456789" {
		t.Fatalf("grouped_id = %q", m.GroupedID)
	}
	if m.Media.ID == "" {
		t.Fatal("media must carry an id that /media/:id can be built from")
	}
	// The id must be stable for the same message, so a URL built on page 1
	// works on a later request.
	again, err := s.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if again.Messages[0].Media.ID != m.Media.ID {
		t.Fatalf("media id is not stable: %q vs %q", again.Messages[0].Media.ID, m.Media.ID)
	}
}

func TestSendAlbumCarriesOrderedItemsAndConfirmedResult(t *testing.T) {
	tg := &fakeTelegram{
		status: tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		albumResult: tgapi.SendAlbumResult{
			ChannelID: 777, GroupedID: 555,
			Messages: []tgapi.Message{{ID: 11, GroupedID: "555"}, {ID: 12, GroupedID: "555"}},
		},
	}
	s := newService(t, tg)
	result, err := s.SendAlbum(context.Background(), rpc.SendAlbumRequest{
		ChannelID: 777,
		Items: []rpc.SendAlbumItem{
			{Media: &rpc.SendMedia{Kind: "photo", MimeType: "image/jpeg", Data: []byte("a")}, Text: "first", Entities: []rpc.Entity{{Type: "hashtag", Offset: 0, Length: 4}}, RandomID: 101},
			{Media: &rpc.SendMedia{Kind: "video", MimeType: "video/mp4", Data: []byte("b"), Duration: 3}, Text: "second", RandomID: 102},
		},
	})
	if err != nil {
		t.Fatalf("SendAlbum: %v", err)
	}
	if len(tg.albumReq.Items) != 2 || string(tg.albumReq.Items[0].Media.Data) != "a" ||
		string(tg.albumReq.Items[1].Media.Data) != "b" || tg.albumReq.Items[0].Text != "first" ||
		tg.albumReq.Items[1].Text != "second" || tg.albumReq.Items[0].RandomID != 101 ||
		tg.albumReq.Items[0].Entities[0].Type != "hashtag" {
		t.Fatalf("telegram album = %+v", tg.albumReq)
	}
	if result.ChannelID != "777" || result.GroupedID != "555" || len(result.Messages) != 2 || result.Messages[1].ID != 12 {
		t.Fatalf("result = %+v", result)
	}
}

func TestMessagesCarryTheOriginalFilename(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan"}},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{
			{
				ID: 36,
				Media: &tgapi.Media{
					Type: "document", Label: "فایل", Filename: "original-app-name.apk",
					MimeType: "application/vnd.android.package-archive", Size: 59797754,
				},
			},
		}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	page, err := s.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	m := page.Messages[0]
	if m.Media == nil {
		t.Fatal("media missing")
	}
	if m.Media.Filename != "original-app-name.apk" {
		t.Fatalf("filename = %q, want %q", m.Media.Filename, "original-app-name.apk")
	}
	// The filename is a second, separate fact: it never becomes the label.
	if m.Media.Label != "فایل" {
		t.Fatalf("label = %q, want %q", m.Media.Label, "فایل")
	}
}

func TestMessagesReportNoFilenameWhenTelegramSentNone(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan"}},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{
			{ID: 24, Media: &tgapi.Media{Type: "audio", Label: "صدا", MimeType: "audio/mpeg", Size: 10}},
		}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	page, err := s.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Messages[0].Media.Filename; got != "" {
		t.Fatalf("filename = %q, want empty", got)
	}
}

func TestMediaFetchesTheBytesForAMessage(t *testing.T) {
	tg := &fakeTelegram{
		status:     tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs:    []tgapi.Channel{{ID: 500, Title: "chan"}},
		mediaBytes: []byte("bytes"),
		mediaType:  "image/jpeg",
		mediaSize:  5,
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	file, err := s.Media(context.Background(), 500, 179, 0, rpc.MediaSlice{})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	defer file.Body.Close()
	data, err := io.ReadAll(file.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bytes" || file.MimeType != "image/jpeg" {
		t.Fatalf("file = %+v (%q)", file, data)
	}
	if file.Length != 5 || file.Size != 5 {
		t.Fatalf("length = %d size = %d", file.Length, file.Size)
	}
}

func TestCustomEmojiNeedsALiveClientAndMapsAMissingOne(t *testing.T) {
	tg := &fakeTelegram{
		status: tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		emoji:  tgapi.EmojiFile{MimeType: "application/x-tgsticker", Data: []byte("abc")},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	page, err := s.CustomEmoji(context.Background(), 5368324170671202286)
	if err != nil {
		t.Fatalf("CustomEmoji: %v", err)
	}
	if tg.gotEmoji != 5368324170671202286 {
		t.Fatalf("document id = %d", tg.gotEmoji)
	}
	if string(page.Data) != "abc" || page.MimeType != "application/x-tgsticker" {
		t.Fatalf("file = %+v", page)
	}

	tg.emojiErr = tgapi.ErrCustomEmojiNotFound
	_, err = s.CustomEmoji(context.Background(), 1)
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeNotFound {
		t.Fatalf("err = %v, want CodeNotFound", err)
	}

	if _, err := s.CustomEmoji(context.Background(), 0); err == nil {
		t.Fatal("a non-positive document id must be refused")
	}
}

func TestMediaRequiresAuthorization(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}
	s := newService(t, tg)
	if _, err := s.Media(context.Background(), 500, 1, 0, rpc.MediaSlice{}); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}
}

func TestMediaRejectsAMessageWithoutAnAttachment(t *testing.T) {
	tg := &fakeTelegram{
		status:   tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs:  []tgapi.Channel{{ID: 500, Title: "chan"}},
		mediaErr: tgapi.ErrMediaNotFound,
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	_, err := s.Media(context.Background(), 500, 179, 0, rpc.MediaSlice{})
	if err == nil {
		t.Fatal("expected an error")
	}
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeNotFound {
		t.Fatalf("err = %v (want not_found)", err)
	}
}

// --- one message ---

func (f *fakeTelegram) Message(_ context.Context, channel tgapi.Channel, messageID int) (tgapi.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oneMessageChannel, f.oneMessageID = channel, messageID
	f.oneMessageCalls++
	return f.oneMessage, f.oneMessageErr
}

func TestSingleMessageReturnsOnePost(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan", Username: "sample"}},
		oneMessage: tgapi.Message{
			ID:    179,
			Text:  "hello",
			Date:  time.Date(2026, 5, 28, 5, 41, 38, 0, time.UTC),
			Media: &tgapi.Media{Type: "photo", Label: "عکس", MimeType: "image/jpeg", Size: 10},
		},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	page, err := s.Message(context.Background(), 500, 179)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != 179 {
		t.Fatalf("messages = %+v", page.Messages)
	}
	m := page.Messages[0]
	if m.Text != "hello" || m.Media == nil || m.Media.MimeType != "image/jpeg" {
		t.Fatalf("message = %+v", m)
	}
	if m.Media.ID != "500:179" {
		t.Fatalf("media id = %q", m.Media.ID)
	}
	if page.Channel.ID != 500 {
		t.Fatalf("channel = %+v", page.Channel)
	}
	if page.Page.Page != 1 || page.Page.HasMore {
		t.Fatalf("page = %+v; a single message is not paginated", page.Page)
	}
}

func TestMessagesCarryViewsAndReactions(t *testing.T) {
	views := 1520
	chosen := true
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan"}},
		hist: tgapi.HistoryPage{Messages: []tgapi.Message{
			{
				ID: 120, Text: "with stats", Views: &views,
				Reactions: []tgapi.Reaction{
					{Type: "emoji", Emoji: "👍", Count: 12},
					{Type: "emoji", Emoji: "❤️", Count: 8, Chosen: &chosen},
					{Type: "custom_emoji", DocumentID: 77, Count: 2},
				},
				Media: &tgapi.Media{Type: "photo", Label: "عکس", MimeType: "image/jpeg", Size: 10},
			},
			{ID: 119, Text: "views only", Views: &views},
			{ID: 118, Text: "bare"},
		}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	page, err := s.Messages(context.Background(), 500, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	withStats := page.Messages[0]
	if withStats.Views == nil || *withStats.Views != 1520 {
		t.Fatalf("views = %v", withStats.Views)
	}
	if len(withStats.Reactions) != 3 ||
		withStats.Reactions[0].Emoji != "👍" ||
		withStats.Reactions[1].Chosen == nil || !*withStats.Reactions[1].Chosen ||
		withStats.Reactions[2].DocumentID != 77 {
		t.Fatalf("reactions = %+v", withStats.Reactions)
	}
	if withStats.Media == nil {
		t.Fatal("media missing alongside views")
	}
	if page.Messages[1].Reactions == nil || len(page.Messages[1].Reactions) != 0 {
		t.Fatalf("views-only reactions = %#v", page.Messages[1].Reactions)
	}
	if bare := page.Messages[2]; bare.Views != nil || bare.Reactions == nil || len(bare.Reactions) != 0 {
		t.Fatalf("bare message = views %v reactions %#v", bare.Views, bare.Reactions)
	}
}

func TestSingleMessageMapsMissingToNotFound(t *testing.T) {
	tg := &fakeTelegram{
		status:        tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs:       []tgapi.Channel{{ID: 500, Title: "chan"}},
		oneMessageErr: tgapi.ErrMessageNotFound,
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	_, err := s.Message(context.Background(), 500, 179)
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeNotFound {
		t.Fatalf("err = %v, want not_found", err)
	}
}

func TestSingleMessageRequiresAuthorization(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}
	s := newService(t, tg)
	if _, err := s.Message(context.Background(), 500, 1); !errors.Is(err, rpc.ErrNeedsAuth) {
		t.Fatalf("err = %v, want ErrNeedsAuth", err)
	}
}

func TestSingleMessageRejectsUnknownChannel(t *testing.T) {
	tg := &fakeTelegram{
		status:  tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		dialogs: []tgapi.Channel{{ID: 500, Title: "chan"}},
	}
	s := newService(t, tg)
	if _, err := s.Channels(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Message(context.Background(), 999, 1); !errors.Is(err, rpc.ErrChannelNotFound) {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
}

// readyFake is a fake client that is authorized and connected.
func readyFake() *fakeTelegram {
	return &fakeTelegram{status: tgapi.Status{
		State: tgapi.StateReady, ThisDC: 2,
		User: &tgapi.Account{ID: 7, Username: "me"},
	}}
}

func TestFindParsesTheQueryAndReturnsChannels(t *testing.T) {
	tg := readyFake()
	tg.findResult = []tgapi.ChannelInfo{{
		ID: 500, Title: "Example", Username: "example",
		Membership:  tgapi.MembershipMember,
		Permissions: tgapi.Permissions{Read: true},
		HasPhoto:    true,
		PhotoID:     77,
	}}
	s := newService(t, tg)

	got, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByUsername, Username: "example"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got.Channels) != 1 {
		t.Fatalf("channels = %+v", got.Channels)
	}
	ch := got.Channels[0]
	if ch.ID != "500" || ch.Title != "Example" || !ch.IsMember || !ch.Permissions.Read {
		t.Fatalf("channel = %+v", ch)
	}
	if !ch.AvatarAvailable || ch.AvatarVersion != 77 {
		t.Fatalf("avatar = %v/%d", ch.AvatarAvailable, ch.AvatarVersion)
	}
	if tg.gotQuery.Kind != tgapi.QueryUsername || tg.gotQuery.Username != "example" {
		t.Fatalf("adapter saw %+v", tg.gotQuery)
	}
}

func TestFindRejectsMalformedQueriesBeforeTouchingTelegram(t *testing.T) {
	tg := readyFake()
	s := newService(t, tg)

	if _, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByUsername, Username: ""}); err == nil {
		t.Fatal("an empty username must be rejected")
	}
	var coded *rpc.Error
	if err := mustCoded(t, s, rpc.FindRequest{Kind: rpc.FindByID, ChannelID: 0}); !errors.As(err, &coded) ||
		coded.Code != rpc.CodeInvalidInput {
		t.Fatalf("err = %v", err)
	}
	if tg.findCalls != 0 {
		t.Fatalf("the adapter was called %d times for invalid input", tg.findCalls)
	}
}

func mustCoded(t *testing.T, s *worker.Service, req rpc.FindRequest) error {
	t.Helper()
	_, err := s.Find(context.Background(), req)
	return err
}

func TestFindMapsUnresolvableIDToItsOwnCode(t *testing.T) {
	tg := readyFake()
	tg.findErr = tgapi.ErrNumericIDUnresolved
	s := newService(t, tg)

	_, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByID, ChannelID: 500})
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodePeerUnresolved {
		t.Fatalf("err = %v, want peer_unresolved", err)
	}

	tg.findErr = tgapi.ErrInviteExpired
	_, err = s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByInvite, Invite: "AbCdEf123456"})
	if !errors.As(err, &coded) || coded.Code != rpc.CodeInviteInvalid {
		t.Fatalf("err = %v, want invite_invalid", err)
	}
}

func TestFindReturnsAnEmptyArrayNotNil(t *testing.T) {
	tg := readyFake()
	s := newService(t, tg)

	got, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByText, Text: "nothing"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Channels == nil {
		t.Fatal("an empty result must be an empty array, not null")
	}
	if len(got.Channels) != 0 {
		t.Fatalf("channels = %+v", got.Channels)
	}
}

func TestFindRequiresAuthorization(t *testing.T) {
	tg := &fakeTelegram{status: tgapi.Status{State: tgapi.StateUnauthorized}}
	s := newService(t, tg)
	if _, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByText, Text: "x"}); err == nil {
		t.Fatal("an unauthorized client must not search")
	}
}

func TestLeaveReportsTelegramConfirmation(t *testing.T) {
	tg := readyFake()
	s := newService(t, tg)

	got, err := s.Leave(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if got.AlreadyAbsent {
		t.Fatal("a confirmed leave must not be reported as nothing to do")
	}
	if len(tg.leaves) != 1 || tg.leaves[0] != 500 {
		t.Fatalf("the adapter was asked to leave %+v", tg.leaves)
	}
}

func TestLeaveOfAChannelTheAccountIsNotInIsNotAnError(t *testing.T) {
	tg := readyFake()
	tg.leaveErr = tgapi.ErrNotAMember
	s := newService(t, tg)

	got, err := s.Leave(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if !got.AlreadyAbsent {
		t.Fatal("an account that is not in the channel was reported as left")
	}
}

func TestLeaveKeepsTheTelegramFailure(t *testing.T) {
	tg := readyFake()
	tg.leaveErr = tgapi.ErrChannelNotFound
	s := newService(t, tg)

	_, err := s.Leave(context.Background(), rpc.ChannelRef{ChannelID: 500})
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeChannelNotFound {
		t.Fatalf("err = %v, want a channel_not_found code", err)
	}
}

func TestLeaveNeedsALiveClient(t *testing.T) {
	tg := readyFake()
	tg.status.State = tgapi.StateDisconnected
	s := newService(t, tg)

	if _, err := s.Leave(context.Background(), rpc.ChannelRef{ChannelID: 500}); !errors.Is(err, rpc.ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
	if len(tg.leaves) != 0 {
		t.Fatal("Telegram must not be asked without a connection")
	}
}

func TestJoinReportsWhatTelegramConfirmed(t *testing.T) {
	tg := readyFake()
	tg.joinResult = tgapi.JoinResult{
		Channel: tgapi.ChannelInfo{ID: 500, Title: "Example", Membership: tgapi.MembershipMember},
		Outcome: tgapi.JoinJoined,
	}
	s := newService(t, tg)

	got, err := s.Join(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got.Outcome != rpc.OutcomeJoined {
		t.Fatalf("outcome = %q", got.Outcome)
	}
	if !got.Channel.IsMember || got.Channel.MembershipStatus != "member" {
		t.Fatalf("channel = %+v", got.Channel)
	}
	if tg.gotRef.ID != 500 {
		t.Fatalf("adapter saw %+v", tg.gotRef)
	}
}

func TestJoinPendingIsNotReportedAsMembership(t *testing.T) {
	tg := readyFake()
	tg.joinResult = tgapi.JoinResult{
		Channel: tgapi.ChannelInfo{ID: 500, Membership: tgapi.MembershipJoinPending},
		Outcome: tgapi.JoinPending,
	}
	s := newService(t, tg)

	got, err := s.Join(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got.Outcome != rpc.OutcomePending {
		t.Fatalf("outcome = %q", got.Outcome)
	}
	if got.Channel.IsMember {
		t.Fatal("a pending request must not report membership")
	}
	if got.Channel.MembershipStatus != "join_pending" {
		t.Fatalf("status = %q", got.Channel.MembershipStatus)
	}
	if got.Channel.Permissions.Read {
		t.Fatal("a pending request must not grant read permission")
	}
}

func TestJoinMapsTelegramErrorsToCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"already participant is not an error", tgapi.ErrAlreadyParticipant, ""},
		{"pending", tgapi.ErrJoinPending, ""},
		{"expired invite", tgapi.ErrInviteExpired, rpc.CodeInviteInvalid},
		{"bad invite", tgapi.ErrInviteInvalid, rpc.CodeInviteInvalid},
		{"limit", tgapi.ErrMembershipLimit, rpc.CodeMembershipLimit},
		{"unresolved", tgapi.ErrNumericIDUnresolved, rpc.CodePeerUnresolved},
		{"not found", tgapi.ErrChannelNotFound, rpc.CodeChannelNotFound},
		{"unconfirmed", tgapi.ErrJoinUnconfirmed, rpc.CodeInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := readyFake()
			tg.joinErr = tc.err
			s := newService(t, tg)

			_, err := s.Join(context.Background(), rpc.ChannelRef{ChannelID: 500})
			if tc.want == "" {
				// Pending and already-participant are states, not errors.
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			var coded *rpc.Error
			if !errors.As(err, &coded) {
				t.Fatalf("err = %v, want a coded error", err)
			}
			if coded.Code != tc.want {
				t.Fatalf("code = %q, want %q", coded.Code, tc.want)
			}
		})
	}
}

func TestJoinRequiresAValidReference(t *testing.T) {
	tg := readyFake()
	s := newService(t, tg)
	if _, err := s.Join(context.Background(), rpc.ChannelRef{}); err == nil {
		t.Fatal("an empty reference must be rejected")
	}
	if tg.joinCalls != 0 {
		t.Fatalf("the adapter was called %d times", tg.joinCalls)
	}
}

func TestChannelReturnsTheResolvedChannel(t *testing.T) {
	tg := readyFake()
	tg.resolved = tgapi.ChannelInfo{
		ID: 500, Title: "Example", Username: "example",
		Membership: tgapi.MembershipMember, HasPhoto: true, PhotoID: 5,
	}
	s := newService(t, tg)

	got, err := s.Channel(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Channel: %v", err)
	}
	if got.Channel.ID != "500" || !got.Channel.AvatarAvailable {
		t.Fatalf("channel = %+v", got.Channel)
	}
}

func TestAvatarReturnsBytesAndMapsAMissingPhoto(t *testing.T) {
	tg := readyFake()
	tg.avatar = tgapi.AvatarFile{MimeType: "image/jpeg", PhotoID: 5, Data: []byte("jpeg")}
	s := newService(t, tg)

	got, err := s.Avatar(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatalf("Avatar: %v", err)
	}
	if string(got.Data) != "jpeg" || got.PhotoID != 5 {
		t.Fatalf("avatar = %+v", got)
	}

	tg.avatarErr = tgapi.ErrNoAvatar
	_, err = s.Avatar(context.Background(), rpc.ChannelRef{ChannelID: 500})
	var coded *rpc.Error
	if !errors.As(err, &coded) || coded.Code != rpc.CodeNoAvatar {
		t.Fatalf("err = %v, want no_avatar", err)
	}
}

func TestPermissionsAreAlwaysBooleansInTheWireShape(t *testing.T) {
	tg := readyFake()
	tg.findResult = []tgapi.ChannelInfo{{ID: 500, Title: "Example"}}
	s := newService(t, tg)

	got, err := s.Find(context.Background(), rpc.FindRequest{Kind: rpc.FindByText, Text: "example"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got.Channels[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	perms, ok := decoded["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions = %T", decoded["permissions"])
	}
	for _, field := range []string{"new", "edit", "delete", "read"} {
		v, ok := perms[field]
		if !ok {
			t.Fatalf("permissions.%s is missing", field)
		}
		if _, ok := v.(bool); !ok {
			t.Fatalf("permissions.%s = %v (%T), want a bool", field, v, v)
		}
	}
}

// The wire answer says whether a photo exists apart from its version: a client
// can report a photo without knowing its id, and the local channel snapshot
// needs the first without the second.
func TestChannelReportsThePhotoApartFromItsVersion(t *testing.T) {
	tg := &fakeTelegram{
		status:   tgapi.Status{State: tgapi.StateReady, User: &tgapi.Account{ID: 1}},
		resolved: tgapi.ChannelInfo{ID: 500, Kind: "channel", HasPhoto: true, PhotoID: 0},
	}
	s := newService(t, tg)
	result, err := s.Channel(context.Background(), rpc.ChannelRef{ChannelID: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Channel.HasPhoto || !result.Channel.AvatarAvailable || result.Channel.AvatarVersion != 0 {
		t.Fatalf("channel = %+v", result.Channel)
	}
}
