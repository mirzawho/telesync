package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// fakeWorker is one scripted upstream worker.
type fakeWorker struct {
	customEmoji  rpc.MediaFile
	customEmojiE error
	lastEmojiID  int64
	mu           sync.Mutex
	health       rpc.Health
	healthE      error
	profile      rpc.Profile
	profileE     error
	channels     rpc.ChannelPage
	chanE        error
	messages     rpc.MessagePage
	msgE         error
	media        rpc.MediaFile
	mediaE       error
	message      rpc.MessagePage
	messageE     error

	calls    int
	lastPage int
	lastPer  int
	lastChan int64

	lastMediaChannel int64
	lastMediaMessage int

	lastMessageChannel int64
	lastMessageID      int

	find     rpc.FindResult
	findE    error
	lastFind rpc.FindRequest
	channel  rpc.ChannelResult
	channelE error
	join     rpc.JoinResult
	joinE    error
	avatar   rpc.AvatarFile
	avatarE  error
	lastRef  rpc.ChannelRef

	leaveCalls int
	leaveIDs   []int64
	leaveE     error

	sendCalls int
	sentReq   rpc.SendRequest
	send      rpc.SendResult
	sendE     error
}

func (f *fakeWorker) Health(context.Context) (rpc.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.health, f.healthE
}

func (f *fakeWorker) Profile(context.Context) (rpc.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.profile, f.profileE
}

func (f *fakeWorker) Channels(_ context.Context, page, perPage int) (rpc.ChannelPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastPage, f.lastPer = page, perPage
	return f.channels, f.chanE
}

func (f *fakeWorker) Messages(_ context.Context, channelID int64, page, perPage int) (rpc.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastChan, f.lastPage, f.lastPer = channelID, page, perPage
	return f.messages, f.msgE
}

func (f *fakeWorker) Message(_ context.Context, channelID int64, messageID int) (rpc.MessagePage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastMessageChannel, f.lastMessageID = channelID, messageID
	return f.message, f.messageE
}

func (f *fakeWorker) CustomEmoji(_ context.Context, documentID int64) (rpc.MediaFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastEmojiID = documentID
	return f.customEmoji, f.customEmojiE
}

func (f *fakeWorker) Media(_ context.Context, channelID int64, messageID, _ int) (rpc.MediaFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastMediaChannel, f.lastMediaMessage = channelID, messageID
	return f.media, f.mediaE
}

func (f *fakeWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCalls++
	f.sentReq = req
	return f.send, f.sendE
}

func (f *fakeWorker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// registry is a static set of workers keyed like the configuration file.
type registry map[string]*fakeWorker

func (r registry) Worker(key string) (gateway.Worker, bool) {
	w, ok := r[key]
	if !ok {
		return nil, false
	}
	return w, true
}

type testEnv struct {
	handler http.Handler
	workers registry
	// session is the credential get and send attach when a test names none:
	// the operator token, which every route accepts.
	session string
	// onCreated, when set, is told the id of every resource the API creates.
	onCreated func(id int64)
	// started is the configuration the handler was built from.
	started gateway.Config
}

// installOnCreated adds a creation hook. It must be called before the first
// request.
func (e *testEnv) installOnCreated(fn func(id int64)) {
	e.onCreated = fn
	e.handler = gateway.New(e.started)
}

func newEnv(t *testing.T, workers registry, mutate ...func(*gateway.Config)) *testEnv {
	t.Helper()
	cfg := gateway.Config{
		Workers:        workers,
		RequestTimeout: 2 * time.Second,
		Now:            func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) },
	}
	cfg.Operator = operatorToken
	for _, fn := range mutate {
		fn(&cfg)
	}
	env := &testEnv{workers: workers, session: operatorToken}
	cfg.OnCreated = func(kind channelstore.ResourceKind, id int64) {
		if env.onCreated != nil {
			env.onCreated(id)
		}
	}
	env.started = cfg
	env.handler = gateway.New(cfg)
	return env
}

// get runs one GET through the handler in-process.
func (e *testEnv) get(t *testing.T, path string, headers ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if req.Header.Get("Authorization") == "" && e.session != "" {
		// The default caller is the permitted test session; a test about
		// authentication supplies its own header.
		req.Header.Set("Authorization", "Bearer "+e.session)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Result()
}

type envelope struct {
	Data json.RawMessage `json:"data"`
	Meta json.RawMessage `json:"meta"`
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func readyWorker() *fakeWorker {
	return &fakeWorker{
		health: rpc.Health{
			Key: "official", Type: "official", State: rpc.StateConnected,
			Authorized: true, Transport: "ready", RPCReady: true, PID: 1, ThisDC: 2,
		},
		profile: rpc.Profile{
			Key: "official", Type: "official", Authorized: true,
			AuthorizationState: rpc.AuthAuthorized,
			User:               &rpc.Account{ID: 7, Username: "me"},
		},
	}
}

func TestHealthRoutesToTheConfiguredWorker(t *testing.T) {
	official := readyWorker()
	cat := readyWorker()
	cat.health.Key = "the-cat"
	cat.health.Type = "custom"
	env := newEnv(t, registry{"official": official, "the-cat": cat})

	resp := env.get(t, "/api/v1/telegram/the-cat/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[envelope](t, resp)
	var data struct {
		Key        string `json:"key"`
		Type       string `json:"type"`
		State      string `json:"state"`
		Authorized bool   `json:"authorized"`
	}
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Key != "the-cat" || data.Type != "custom" || data.State != "connected" || !data.Authorized {
		t.Fatalf("data = %+v", data)
	}
	if official.callCount() != 0 {
		t.Fatal("the official worker must not be called for the-cat")
	}
	if cat.callCount() != 1 {
		t.Fatalf("the-cat calls = %d", cat.callCount())
	}
	if len(body.Meta) == 0 {
		t.Error("every response should carry meta")
	}
}

func TestUnknownKeyIs404AndNeverFallsBack(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/nope/health")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "unknown_client" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if official.callCount() != 0 {
		t.Fatal("an unknown key must never reach a different worker")
	}
}

func TestUnavailableWorkerIs503NotASubstitute(t *testing.T) {
	official := readyWorker()
	official.healthE = errors.Join(rpc.ErrUnavailable, errors.New("dial unix: no such file"))
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/health")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "worker_unavailable" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if body.Error.RequestID == "" {
		t.Error("an error response must carry a request id for correlation")
	}
}

func TestWorkerTimeoutIs504(t *testing.T) {
	official := readyWorker()
	official.chanE = errors.Join(rpc.ErrTimeout, context.DeadlineExceeded)
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels")
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
}

func TestNeedsAuthIs409WithAnExplicitState(t *testing.T) {
	official := readyWorker()
	official.chanE = rpc.ErrNeedsAuth
	official.health.State = rpc.StateNeedsAuth
	official.health.Authorized = false
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "needs_auth" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestNotConnectedIs503(t *testing.T) {
	official := readyWorker()
	official.profileE = rpc.ErrNotConnected
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/profile")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "not_connected" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestChannelNotFoundIs404(t *testing.T) {
	official := readyWorker()
	official.msgE = rpc.ErrChannelNotFound
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/999/messages")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "channel_not_found" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestRateLimitIs429WithRetryAfter(t *testing.T) {
	official := readyWorker()
	official.chanE = rpc.NewError(rpc.CodeRateLimited, "slow down", 12)
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "12" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestInternalWorkerErrorIs502WithoutLeakingDetails(t *testing.T) {
	official := readyWorker()
	official.chanE = errors.New("rpc: some internal detail with api_hash=deadbeef")
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if strings.Contains(body.Error.Message, "api_hash") {
		t.Fatalf("the error message leaks internals: %q", body.Error.Message)
	}
}

func TestChannelsPassValidatedPagination(t *testing.T) {
	official := readyWorker()
	official.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{{ID: 1, Title: "a"}},
		Page:     rpc.PageInfo{Page: 2, PerPage: 5, Total: 12, HasMore: true, NextPage: 3},
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels?page=2&per_page=5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	official.mu.Lock()
	page, per := official.lastPage, official.lastPer
	official.mu.Unlock()
	if page != 2 || per != 5 {
		t.Fatalf("worker saw page=%d per_page=%d", page, per)
	}
	body := decode[envelope](t, resp)
	var meta struct {
		Page     int  `json:"page"`
		PerPage  int  `json:"per_page"`
		Total    int  `json:"total"`
		HasMore  bool `json:"has_more"`
		NextPage int  `json:"next_page"`
	}
	if err := json.Unmarshal(body.Meta, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Page != 2 || meta.PerPage != 5 || meta.Total != 12 || !meta.HasMore || meta.NextPage != 3 {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestPaginationValidation(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})

	for _, q := range []string{
		"page=0", "page=-1", "page=abc",
		"per_page=0", "per_page=101", "per_page=abc",
		"perpage=10", "page=1&page=2",
	} {
		t.Run(q, func(t *testing.T) {
			resp := env.get(t, "/api/v1/telegram/official/channels?"+q)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d for %q", resp.StatusCode, q)
			}
			body := decode[errorEnvelope](t, resp)
			if body.Error.Code != "invalid_input" {
				t.Fatalf("code = %q", body.Error.Code)
			}
		})
	}
	if official.callCount() != 0 {
		t.Fatal("an invalid request must never reach a worker")
	}
}

func TestDefaultsArePageOneTwentyPerPage(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	env.get(t, "/api/v1/telegram/official/channels")
	official.mu.Lock()
	defer official.mu.Unlock()
	if official.lastPage != 1 || official.lastPer != 20 {
		t.Fatalf("defaults = %d/%d", official.lastPage, official.lastPer)
	}
}

func TestMessagesRoute(t *testing.T) {
	official := readyWorker()
	official.messages = rpc.MessagePage{
		Channel:  rpc.Channel{ID: 500, Title: "chan"},
		Messages: []rpc.Message{{ID: 9, Text: "hello"}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 20, Total: 1},
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/500/messages?page=1&per_page=20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	official.mu.Lock()
	ch := official.lastChan
	official.mu.Unlock()
	if ch != 500 {
		t.Fatalf("channel id = %d", ch)
	}
	body := decode[envelope](t, resp)
	var data struct {
		Channel  *rpc.Channel  `json:"channel"`
		Messages []rpc.Message `json:"messages"`
	}
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Channel != nil {
		t.Fatalf("channel must not be repeated in the body: %+v", data.Channel)
	}
	if len(data.Messages) != 1 || data.Messages[0].Text != "hello" {
		t.Fatalf("data = %+v", data)
	}
}

func TestMessagesRejectInvalidChannelIDs(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	for _, path := range []string{
		"/api/v1/telegram/official/channels/abc/messages",
		"/api/v1/telegram/official/channels/0/messages",
		"/api/v1/telegram/official/channels/-1/messages",
	} {
		resp := env.get(t, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", path, resp.StatusCode)
		}
	}
	if official.callCount() != 0 {
		t.Fatal("invalid ids must never reach a worker")
	}
}

func TestProfileRoute(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[envelope](t, resp)
	var data rpc.Profile
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.AuthorizationState != rpc.AuthAuthorized || data.User == nil || data.User.Username != "me" {
		t.Fatalf("profile = %+v", data)
	}
}

func TestProfileNeverExposesSecrets(t *testing.T) {
	official := readyWorker()
	official.profile.User = &rpc.Account{ID: 7, Username: "me"}
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[map[string]json.RawMessage](t, resp)
	raw := string(body["data"])
	for _, forbidden := range []string{"hash", "session", "auth_key", "password", "phone"} {
		if strings.Contains(strings.ToLower(raw), forbidden) {
			t.Fatalf("the profile response contains %q: %s", forbidden, raw)
		}
	}
}

func TestMethodsOtherThanGetAreRejected(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut, http.MethodPatch} {
		req, _ := http.NewRequest(method, "/api/v1/telegram/official/health", nil)
		req.Header.Set("Authorization", "Bearer "+operatorToken)
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, rec.Code)
		}
	}
	if official.callCount() != 0 {
		t.Fatal("a rejected method must never reach a worker")
	}
}

func TestGatewayHealthIsSeparateFromClientHealth(t *testing.T) {
	official := readyWorker()
	official.healthE = errors.Join(rpc.ErrUnavailable, errors.New("down"))
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gateway health = %d; a live gateway stays ok when a worker is down", resp.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if body.Status != "ok" {
		t.Fatalf("status = %q", body.Status)
	}
}

func TestOperatorTokenIsRequiredWhenConfigured(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official}, func(c *gateway.Config) {
		c.Operator = "s3cret-token"
	})
	env.session = ""

	if resp := env.get(t, "/api/v1/telegram/official/health"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token = %d, want 401", resp.StatusCode)
	}
	if resp := env.get(t, "/api/v1/telegram/official/health", "Authorization", "Bearer wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", resp.StatusCode)
	}
	if official.callCount() != 0 {
		t.Fatal("an unauthenticated request must never reach a worker")
	}
	if resp := env.get(t, "/api/v1/telegram/official/health", "Authorization", "Bearer s3cret-token"); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", resp.StatusCode)
	}

	// The gateway health endpoint stays reachable for liveness checks, with or
	// without a credential; it reveals nothing about the workers.
	if resp := env.get(t, "/healthz"); resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz with auth enabled = %d", resp.StatusCode)
	}
}

func TestAuthIsCheckedBeforeKeyLookup(t *testing.T) {
	// An unauthenticated caller must not be able to tell a configured key from
	// an unknown one.
	env := newEnv(t, registry{"official": readyWorker()}, func(c *gateway.Config) {
		c.Operator = "tok"
	})
	env.session = ""
	known := env.get(t, "/api/v1/telegram/official/health")
	unknown := env.get(t, "/api/v1/telegram/not-configured/health")
	if known.StatusCode != http.StatusUnauthorized || unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("known=%d unknown=%d, both must be 401", known.StatusCode, unknown.StatusCode)
	}
}

func TestUnauthorizedErrorBodyDoesNotEchoTheToken(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official}, func(c *gateway.Config) {
		c.Operator = "s3cret-token"
	})
	env.session = ""
	resp := env.get(t, "/api/v1/telegram/official/health", "Authorization", "Bearer s3cret-tokenx")
	body := decode[errorEnvelope](t, resp)
	if strings.Contains(body.Error.Message, "s3cret") {
		t.Fatalf("the error echoes the token: %q", body.Error.Message)
	}
}

func TestUnknownRouteAndClientKeyValidation(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	cases := []struct {
		path string
		want int
	}{
		{"/api/v1/telegram/official", http.StatusNotFound},
		{"/api/v1/telegram/official/unknown", http.StatusNotFound},
		{"/api/v1/telegram/official/channels/1/unknown", http.StatusNotFound},
		{"/api/v1/telegram/official/channels/1/messages/extra/deeper", http.StatusNotFound},
		{"/api/v1/nope", http.StatusNotFound},
	}
	for _, tc := range cases {
		resp := env.get(t, tc.path)
		if resp.StatusCode != tc.want {
			t.Errorf("%s = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
}

func TestEveryResponseHasARequestID(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/health")
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("responses must carry X-Request-Id")
	}
}

func TestRequestIDIsEchoed(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/health", "X-Request-Id", "abc-123")
	if got := resp.Header.Get("X-Request-Id"); got != "abc-123" {
		t.Fatalf("request id = %q", got)
	}
}

func TestContextCancellationStopsTheWorkerCall(t *testing.T) {
	blocking := &blockingWorker{release: make(chan struct{})}
	cfg := gateway.Config{
		Workers:        blockingRegistry{blocking},
		RequestTimeout: 50 * time.Millisecond,
	}
	cfg.Operator = operatorToken
	env := &testEnv{handler: gateway.New(cfg)}
	start := time.Now()
	resp := env.get(t, "/api/v1/telegram/official/health", "Authorization", "Bearer "+operatorToken)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("the gateway did not bound the request: %s", elapsed)
	}
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
	close(blocking.release)
}

// blockingWorker never answers until released or its context ends.
type blockingWorker struct {
	release chan struct{}
}

func (b *blockingWorker) Health(ctx context.Context) (rpc.Health, error) {
	select {
	case <-ctx.Done():
		return rpc.Health{}, ctx.Err()
	case <-b.release:
		return rpc.Health{Key: "official"}, nil
	}
}

func (b *blockingWorker) Profile(ctx context.Context) (rpc.Profile, error) {
	<-ctx.Done()
	return rpc.Profile{}, ctx.Err()
}

func (b *blockingWorker) Channels(ctx context.Context, _, _ int) (rpc.ChannelPage, error) {
	<-ctx.Done()
	return rpc.ChannelPage{}, ctx.Err()
}

func (b *blockingWorker) Messages(ctx context.Context, _ int64, _, _ int) (rpc.MessagePage, error) {
	<-ctx.Done()
	return rpc.MessagePage{}, ctx.Err()
}

func (b *blockingWorker) Message(ctx context.Context, _ int64, _ int) (rpc.MessagePage, error) {
	<-ctx.Done()
	return rpc.MessagePage{}, ctx.Err()
}

func (b *blockingWorker) Media(ctx context.Context, _ int64, _, _ int) (rpc.MediaFile, error) {
	<-ctx.Done()
	return rpc.MediaFile{}, ctx.Err()
}

func (b *blockingWorker) Find(ctx context.Context, _ rpc.FindRequest) (rpc.FindResult, error) {
	<-ctx.Done()
	return rpc.FindResult{}, ctx.Err()
}

func (b *blockingWorker) Channel(ctx context.Context, _ rpc.ChannelRef) (rpc.ChannelResult, error) {
	<-ctx.Done()
	return rpc.ChannelResult{}, ctx.Err()
}

func (b *blockingWorker) Join(ctx context.Context, _ rpc.ChannelRef) (rpc.JoinResult, error) {
	<-ctx.Done()
	return rpc.JoinResult{}, ctx.Err()
}

func (b *blockingWorker) Avatar(ctx context.Context, _ rpc.ChannelRef) (rpc.AvatarFile, error) {
	<-ctx.Done()
	return rpc.AvatarFile{}, ctx.Err()
}

func (b *blockingWorker) SendMessage(ctx context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	<-ctx.Done()
	return rpc.SendResult{}, ctx.Err()
}

func (b *blockingWorker) CustomEmoji(ctx context.Context, _ int64) (rpc.MediaFile, error) {
	<-ctx.Done()
	return rpc.MediaFile{}, ctx.Err()
}

type blockingRegistry struct{ w gateway.Worker }

func (r blockingRegistry) Worker(key string) (gateway.Worker, bool) {
	if key != "official" {
		return nil, false
	}
	return r.w, true
}

func TestConfiguredClientsWithoutAnEnabledWorkerAre404(t *testing.T) {
	// The registry is built from enabled clients only; a disabled key is not
	// registered, so requests for it are 404 rather than a silent fallback.
	env := newEnv(t, registry{"official": readyWorker()})
	resp := env.get(t, "/api/v1/telegram/disabled-client/health")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestKeysAreCaseSensitive(t *testing.T) {
	env := newEnv(t, registry{"official": readyWorker()})
	if resp := env.get(t, "/api/v1/telegram/Official/health"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestClientTypeIsReportedNotGuessed(t *testing.T) {
	cat := readyWorker()
	cat.profile.Type = "custom"
	env := newEnv(t, registry{"the-cat": cat})
	resp := env.get(t, "/api/v1/telegram/the-cat/profile")
	body := decode[envelope](t, resp)
	var data rpc.Profile
	if err := json.Unmarshal(body.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Type != "custom" {
		t.Fatalf("profile = %+v", data)
	}
}

func TestProfileDoesNotExposeTheBackendAddress(t *testing.T) {
	// The worker knows the custom backend's endpoint; the public API must not
	// publish infrastructure addresses.
	cat := readyWorker()
	cat.profile.Type = "custom"
	cat.profile.Endpoint = "mt.backend.test:8443"
	env := newEnv(t, registry{"the-cat": cat})

	resp := env.get(t, "/api/v1/telegram/the-cat/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw := strings.ToLower(string(decode[struct {
		Data json.RawMessage `json:"data"`
	}](t, resp).Data))
	if strings.Contains(raw, "endpoint") || strings.Contains(raw, "backend.test") {
		t.Fatalf("the profile exposes the backend address: %s", raw)
	}
}

func TestConfigIsAuthoritativeForRouting(t *testing.T) {
	// A key that exists only in the registry is routable; nothing in the
	// gateway names "official" or "the-cat".
	third := readyWorker()
	third.health.Key = "third"
	env := newEnv(t, registry{"third": third})
	if resp := env.get(t, "/api/v1/telegram/third/health"); resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	_ = appconfig.Official
}

func TestMisspelledResourceExplainsTheCanonicalRoute(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})

	// The singular form is a common typo; the 404 should say what the real
	// path is instead of leaving the caller to guess.
	resp := env.get(t, "/api/v1/telegram/official/channel/1671633563/messages")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "not_found" {
		t.Fatalf("code = %q", body.Error.Code)
	}
	if !strings.Contains(body.Error.Message, "/channels/") {
		t.Fatalf("the message should name the canonical route: %q", body.Error.Message)
	}
	if official.callCount() != 0 {
		t.Fatal("a misspelled route must never reach a worker")
	}
}

func TestUnknownResourceStillGetsAPlain404(t *testing.T) {
	env := newEnv(t, registry{"official": readyWorker()})
	resp := env.get(t, "/api/v1/telegram/official/widgets")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if strings.Contains(body.Error.Message, "/channels/") {
		t.Fatalf("an unrelated resource must not be hinted at: %q", body.Error.Message)
	}
}

func TestMessagesReturnTextAndMediaExactly(t *testing.T) {
	// A real post: Persian text with newlines and emoji, plus a photo. The
	// public response must carry the text verbatim and the media descriptor.
	const text = "🟣با درود و احترام\n\nبه اطلاع می‌رساند ...\n\nO3 | Coworking Space ☄️\n@O3cowork"
	at := time.Date(2026, 5, 28, 5, 41, 38, 0, time.UTC)

	official := readyWorker()
	official.messages = rpc.MessagePage{
		Channel: rpc.Channel{ID: 1671633563, Title: "O3"},
		Messages: []rpc.Message{{
			ID:    179,
			Date:  at,
			Text:  text,
			Media: &rpc.Media{Type: "photo", Label: "عکس"},
		}},
		Page: rpc.PageInfo{Page: 1, PerPage: 20, Total: 1},
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/1671633563/messages?page=1&per_page=20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Messages []map[string]any `json:"messages"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(body.Data.Messages) != 1 {
		t.Fatalf("messages = %d", len(body.Data.Messages))
	}
	msg := body.Data.Messages[0]
	if got, _ := msg["id"].(float64); int(got) != 179 {
		t.Errorf("id = %v", msg["id"])
	}
	if got, _ := msg["text"].(string); got != text {
		t.Errorf("text was altered:\n got: %q\nwant: %q", got, text)
	}
	if got, _ := msg["date"].(string); got != "2026-05-28T05:41:38Z" {
		t.Errorf("date = %v", msg["date"])
	}
	media, ok := msg["media"].(map[string]any)
	if !ok {
		t.Fatalf("media is missing: %v", msg)
	}
	if media["type"] != "photo" || media["label"] != "عکس" {
		t.Errorf("media = %v", media)
	}
}

func TestMessagesMetaDoesNotClaimATotal(t *testing.T) {
	// Telegram does not report how many messages a channel has in
	// getHistory, so page 1 of 20 messages must not say "total": 0 — that
	// reads as "this channel has no messages".
	official := readyWorker()
	official.messages = rpc.MessagePage{
		Channel:  rpc.Channel{ID: 500, Title: "chan"},
		Messages: []rpc.Message{{ID: 1, Text: "a"}, {ID: 2, Text: "b"}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 20, HasMore: true, NextPage: 2},
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/500/messages?page=1&per_page=20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if _, ok := body.Meta["total"]; ok {
		t.Fatalf("messages meta must not invent a total: %v", body.Meta)
	}
	if got, _ := body.Meta["page"].(float64); int(got) != 1 {
		t.Errorf("page = %v", body.Meta["page"])
	}
	if got, _ := body.Meta["per_page"].(float64); int(got) != 20 {
		t.Errorf("per_page = %v", body.Meta["per_page"])
	}
	if body.Meta["has_more"] != true {
		t.Errorf("has_more = %v", body.Meta["has_more"])
	}
}

func TestChannelsMetaStillReportsATotal(t *testing.T) {
	official := readyWorker()
	official.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{{ID: 1, Title: "a"}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 20, Total: 12},
	}
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/channels?page=1&per_page=20")
	var body struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got, _ := body.Meta["total"].(float64); int(got) != 12 {
		t.Fatalf("channels total = %v, want 12", body.Meta["total"])
	}
}

func TestMediaDescriptorCarriesAFetchableURL(t *testing.T) {
	official := readyWorker()
	official.messages = rpc.MessagePage{
		Channel: rpc.Channel{ID: 1671633563, Title: "O3"},
		Messages: []rpc.Message{{
			ID:   179,
			Text: "x",
			Media: &rpc.Media{
				Type: "photo", Label: "عکس", MimeType: "image/jpeg", Size: 4242,
			},
		}},
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/1671633563/messages?page=1&per_page=20")
	rawBody := readAll(t, resp)
	t.Logf("RAW %s", rawBody)
	var body struct {
		Data struct {
			Messages []struct {
				Media struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Label    string `json:"label"`
					MimeType string `json:"mime_type"`
					Size     int64  `json:"size"`
					URL      string `json:"url"`
				} `json:"media"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.NewDecoder(strings.NewReader(rawBody)).Decode(&body); err != nil {
		t.Fatal(err)
	}

	m := body.Data.Messages[0].Media
	t.Logf("GOT %+v", m)
	if m.Type != "photo" || m.Label != "عکس" || m.MimeType != "image/jpeg" || m.Size != 4242 {
		t.Fatalf("media = %+v", m)
	}
	// A path below the API root, so a client joins it to the root it talks to
	// and not to the site's /api/v1 (which, behind the /admin mount, would be
	// /admin/api/v1/api/v1/...).
	want := "/telegram/official/channels/1671633563/messages/179/media"
	if m.URL != want {
		t.Fatalf("url = %q, want %q", m.URL, want)
	}
	if u := "/api/v1" + m.URL; u != "/api/v1/telegram/official/channels/1671633563/messages/179/media" {
		t.Fatalf("joined url = %q", u)
	}
}

func TestMediaEndpointStreamsTheBytes(t *testing.T) {
	official := readyWorker()
	official.media = rpc.MediaFile{MimeType: "image/jpeg", Data: []byte("jpeg-bytes")}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/1671633563/messages/179/media")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("content type = %q", got)
	}
	if resp.Header.Get("Cache-Control") == "" {
		t.Error("media responses should be cacheable")
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "jpeg-bytes" {
		t.Fatalf("body = %q", data)
	}
	official.mu.Lock()
	ch, msg := official.lastMediaChannel, official.lastMediaMessage
	official.mu.Unlock()
	if ch != 1671633563 || msg != 179 {
		t.Fatalf("worker saw channel=%d message=%d", ch, msg)
	}
}

func TestMediaEndpointMapsNoMediaTo404(t *testing.T) {
	official := readyWorker()
	official.mediaE = rpc.NewError(rpc.CodeNotFound, "This message has no downloadable media.", 0)
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/1/messages/2/media")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "not_found" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestMediaEndpointRejectsBadIDs(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	for _, path := range []string{
		"/api/v1/telegram/official/channels/0/messages/1/media",
		"/api/v1/telegram/official/channels/1/messages/0/media",
		"/api/v1/telegram/official/channels/1/messages/abc/media",
	} {
		resp := env.get(t, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", path, resp.StatusCode)
		}
	}
	if official.callCount() != 0 {
		t.Fatal("invalid ids must never reach a worker")
	}
}

func TestMediaEndpointRequiresAuth(t *testing.T) {
	official := readyWorker()
	official.media = rpc.MediaFile{MimeType: "image/jpeg", Data: []byte("x")}
	env := newEnv(t, registry{"official": official}, func(c *gateway.Config) {
		c.Operator = "tok"
	})
	env.session = ""
	resp := env.get(t, "/api/v1/telegram/official/channels/1/messages/2/media")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestSingleMessageRoute(t *testing.T) {
	official := readyWorker()
	official.message = rpc.MessagePage{
		Channel: rpc.Channel{ID: 1671633563, Title: "O3"},
		Messages: []rpc.Message{{
			ID:   179,
			Text: "hello",
			Media: &rpc.Media{
				Type: "photo", Label: "عکس", MimeType: "image/jpeg", Size: 10,
			},
		}},
		Page: rpc.PageInfo{Page: 1, PerPage: 1, Total: 1},
	}
	env := newEnv(t, registry{"official": official})

	resp := env.get(t, "/api/v1/telegram/official/channels/1671633563/messages/179")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Channel  *rpc.Channel  `json:"channel"`
			Messages []rpc.Message `json:"messages"`
		} `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if body.Data.Channel != nil {
		t.Fatalf("channel must not be repeated in the body: %+v", body.Data.Channel)
	}
	if len(body.Data.Messages) != 1 {
		t.Fatalf("data = %+v", body.Data)
	}
	if body.Data.Messages[0].ID != 179 {
		t.Fatalf("message = %+v", body.Data.Messages[0])
	}
	official.mu.Lock()
	ch, msg := official.lastMessageChannel, official.lastMessageID
	official.mu.Unlock()
	if ch != 1671633563 || msg != 179 {
		t.Fatalf("worker saw channel=%d message=%d", ch, msg)
	}
}

func TestSingleMessageMediaCarriesAURL(t *testing.T) {
	official := readyWorker()
	official.message = rpc.MessagePage{
		Channel: rpc.Channel{ID: 500, Title: "chan"},
		Messages: []rpc.Message{{
			ID:    179,
			Media: &rpc.Media{Type: "photo", Label: "عکس"},
		}},
	}
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/channels/500/messages/179")
	var body struct {
		Data struct {
			Messages []struct {
				Media struct {
					URL string `json:"url"`
				} `json:"media"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	want := "/telegram/official/channels/500/messages/179/media"
	if body.Data.Messages[0].Media.URL != want {
		t.Fatalf("url = %q, want %q", body.Data.Messages[0].Media.URL, want)
	}
}

func TestSingleMessageMissingIs404(t *testing.T) {
	official := readyWorker()
	official.messageE = rpc.NewError(rpc.CodeNotFound, "No message with that id exists in this channel.", 0)
	env := newEnv(t, registry{"official": official})
	resp := env.get(t, "/api/v1/telegram/official/channels/500/messages/999999")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[errorEnvelope](t, resp)
	if body.Error.Code != "not_found" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestSingleMessageRejectsBadIDs(t *testing.T) {
	official := readyWorker()
	env := newEnv(t, registry{"official": official})
	for _, path := range []string{
		"/api/v1/telegram/official/channels/0/messages/1",
		"/api/v1/telegram/official/channels/500/messages/0",
		"/api/v1/telegram/official/channels/500/messages/abc",
	} {
		resp := env.get(t, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", path, resp.StatusCode)
		}
	}
	if official.callCount() != 0 {
		t.Fatal("invalid ids must never reach a worker")
	}
}

// --- channel discovery, join and avatar ---

func (f *fakeWorker) Find(_ context.Context, req rpc.FindRequest) (rpc.FindResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastFind = req
	return f.find, f.findE
}

func (f *fakeWorker) Channel(_ context.Context, ref rpc.ChannelRef) (rpc.ChannelResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastRef = ref
	return f.channel, f.channelE
}

func (f *fakeWorker) Join(_ context.Context, ref rpc.ChannelRef) (rpc.JoinResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastRef = ref
	return f.join, f.joinE
}

func (f *fakeWorker) LeaveChannel(_ context.Context, channelID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.leaveCalls++
	f.leaveIDs = append(f.leaveIDs, channelID)
	return f.leaveE
}

func (f *fakeWorker) Avatar(_ context.Context, ref rpc.ChannelRef) (rpc.AvatarFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastRef = ref
	return f.avatar, f.avatarE
}

func (e *testEnv) do(t *testing.T, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.session != "" {
		req.Header.Set("Authorization", "Bearer "+e.session)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Result()
}

type findBody struct {
	Data struct {
		Channels []struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			Title            string `json:"title"`
			Username         string `json:"username"`
			Description      string `json:"description"`
			AvatarAvailable  bool   `json:"avatar_available"`
			AvatarURL        string `json:"avatar_url"`
			IsMember         bool   `json:"is_member"`
			IsManaged        bool   `json:"is_managed"`
			MembershipStatus string `json:"membership_status"`
			Permissions      struct {
				New    *bool `json:"new"`
				Edit   *bool `json:"edit"`
				Delete *bool `json:"delete"`
				Read   *bool `json:"read"`
			} `json:"permissions"`
		} `json:"channels"`
	} `json:"data"`
	Meta struct {
		Count int `json:"count"`
	} `json:"meta"`
}

func TestFindRoutesByClientKeyAndReturnsAnArray(t *testing.T) {
	official := readyWorker()
	cat := readyWorker()
	cat.find = rpc.FindResult{Channels: []rpc.ChannelInfo{{
		ID: "500", Type: "channel", Title: "Example", Username: "example",
		IsMember: true, MembershipStatus: "member", AvatarAvailable: true,
		Permissions: rpc.Permissions{Read: true},
	}}}
	env := newEnv(t, registry{"official": official, "the-cat": cat})

	resp := env.get(t, "/api/v1/telegram/the-cat/channels/find?q=@example")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body findBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(body.Data.Channels) != 1 {
		t.Fatalf("channels = %+v", body.Data.Channels)
	}
	ch := body.Data.Channels[0]
	if ch.ID != "500" || ch.Title != "Example" {
		t.Fatalf("channel = %+v", ch)
	}
	if ch.AvatarURL != "/api/v1/telegram/the-cat/channels/500/avatar" {
		t.Fatalf("avatar url = %q", ch.AvatarURL)
	}
	if ch.Permissions.Read == nil || !*ch.Permissions.Read {
		t.Fatalf("permissions.read = %v", ch.Permissions.Read)
	}
	if official.callCount() != 0 {
		t.Fatal("the official worker must not be called")
	}
	if cat.lastFind.Username != "example" {
		t.Fatalf("worker saw %+v", cat.lastFind)
	}
	if body.Meta.Count != 1 {
		t.Fatalf("count = %d", body.Meta.Count)
	}
}

func TestFindPermissionsAreAlwaysBooleans(t *testing.T) {
	worker := readyWorker()
	worker.find = rpc.FindResult{Channels: []rpc.ChannelInfo{{ID: "500", Type: "channel", Title: "X"}}}
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/find?q=example")
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	data := parsed["data"].(map[string]any)
	channels := data["channels"].([]any)
	perms := channels[0].(map[string]any)["permissions"].(map[string]any)
	for _, field := range []string{"new", "edit", "delete", "read"} {
		v, ok := perms[field]
		if !ok {
			t.Fatalf("permissions.%s is missing from %s", field, raw)
		}
		if _, ok := v.(bool); !ok {
			t.Fatalf("permissions.%s = %v (%T), want a bool", field, v, v)
		}
	}
}

func TestFindRejectsAMissingOrOversizedQuery(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker})

	for _, path := range []string{
		"/api/v1/telegram/official/channels/find",
		"/api/v1/telegram/official/channels/find?q=",
		"/api/v1/telegram/official/channels/find?q=" + strings.Repeat("a", 300),
	} {
		resp := env.get(t, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	if worker.callCount() != 0 {
		t.Fatal("invalid queries must not reach the worker")
	}
}

func TestFindIdentifiesQueryForms(t *testing.T) {
	tests := []struct {
		q        string
		kind     string
		username string
		invite   string
		id       int64
		text     string
	}{
		{q: "@telegram", kind: rpc.FindByUsername, username: "telegram"},
		{q: "https://t.me/telegram", kind: rpc.FindByUsername, username: "telegram"},
		{q: "https://t.me/+AbCdEf123456", kind: rpc.FindByInvite, invite: "AbCdEf123456"},
		{q: "1671633563", kind: rpc.FindByID, id: 1671633563},
		{q: "technology", kind: rpc.FindByText, text: "technology"},
	}
	for _, tc := range tests {
		t.Run(tc.q, func(t *testing.T) {
			worker := readyWorker()
			worker.find = rpc.FindResult{Channels: []rpc.ChannelInfo{}}
			env := newEnv(t, registry{"official": worker})

			resp := env.get(t, "/api/v1/telegram/official/channels/find?q="+url.QueryEscape(tc.q))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			resp.Body.Close()

			got := worker.lastFind
			if got.Kind != tc.kind {
				t.Fatalf("kind = %q, want %q", got.Kind, tc.kind)
			}
			switch tc.kind {
			case rpc.FindByUsername:
				if got.Username != tc.username {
					t.Fatalf("username = %q", got.Username)
				}
			case rpc.FindByInvite:
				if got.Invite != tc.invite {
					t.Fatalf("invite = %q", got.Invite)
				}
			case rpc.FindByID:
				if got.ChannelID != tc.id {
					t.Fatalf("id = %d", got.ChannelID)
				}
			case rpc.FindByText:
				if got.Text != tc.text {
					t.Fatalf("text = %q", got.Text)
				}
			}
		})
	}
}

func TestFindReturnsAnEmptyArrayNotAnObject(t *testing.T) {
	worker := readyWorker()
	worker.find = rpc.FindResult{Channels: []rpc.ChannelInfo{}}
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/find?q=zzz")
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(string(raw), `"channels":[]`) {
		t.Fatalf("body = %s, want an empty array", raw)
	}
}

// fakeStore is a scripted managed-channel store.
type fakeStore struct {
	mu       sync.Mutex
	channels map[string]ManagedRecord
	err      error
	listErr  error
	upserts  int
	gets     int
	// managedIDsCalls counts reads, so a test can prove listing never asks.
	managedIDsCalls int
}

// ManagedRecord mirrors channelstore.Channel for the fake.
type ManagedRecord = channelstore.Channel

func newStore() *fakeStore { return &fakeStore{channels: map[string]ManagedRecord{}} }

func recordKey(clientKey string, id int64) string {
	return clientKey + "/" + strconv.FormatInt(id, 10)
}

func (f *fakeStore) Get(_ context.Context, clientKey string, id int64) (ManagedRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.err != nil {
		return ManagedRecord{}, false, f.err
	}
	rec, ok := f.channels[recordKey(clientKey, id)]
	return rec, ok, nil
}

func (f *fakeStore) ManagedIDs(_ context.Context, clientKey string) (map[int64]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.managedIDsCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := map[int64]bool{}
	for k, rec := range f.channels {
		if rec.ClientKey == clientKey {
			out[rec.ChannelID] = true
			_ = k
		}
	}
	return out, nil
}

func (f *fakeStore) Upsert(_ context.Context, ch ManagedRecord) (ManagedRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return ManagedRecord{}, f.err
	}
	k := recordKey(ch.ClientKey, ch.ChannelID)
	existing, ok := f.channels[k]
	f.upserts++
	if ok {
		ch.ID = existing.ID
		ch.CreatedAt = existing.CreatedAt
	} else {
		ch.ID = int64(len(f.channels) + 1)
		ch.CreatedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	}
	ch.UpdatedAt = ch.CreatedAt.Add(time.Minute)
	f.channels[k] = ch
	return ch, nil
}

func TestJoinPersistsAConfirmedMembership(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.join = rpc.JoinResult{
		Channel: rpc.ChannelInfo{
			ID: "500", Type: "channel", Title: "Example", Username: "example",
			IsMember: true, MembershipStatus: "member", AvatarAvailable: true,
			HasPhoto: true, AvatarVersion: 9, Verified: true, Scam: true,
			ParticipantsCount: intPtr(42),
			Permissions:       rpc.Permissions{Read: true},
		},
		Outcome: rpc.OutcomeJoined,
	}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Channel struct {
				ID        string `json:"id"`
				IsMember  bool   `json:"is_member"`
				IsManaged bool   `json:"is_managed"`
			} `json:"channel"`
			Result string `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if body.Data.Result != "joined_and_added_to_management" {
		t.Fatalf("result = %q", body.Data.Result)
	}
	if body.Data.Channel.ID != "500" || !body.Data.Channel.IsManaged || !body.Data.Channel.IsMember {
		t.Fatalf("channel = %+v", body.Data.Channel)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d", store.upserts)
	}
	saved := store.channels[recordKey("official", 500)]
	if saved.MembershipStatus != "member" || saved.Title != "Example" {
		t.Fatalf("saved = %+v", saved)
	}
	// Everything the canonical channel object needs is written by the join, so
	// the local detail reads a complete snapshot and never has to guess.
	if !saved.NoticesKnown || !saved.Verified || !saved.Scam || saved.Fake {
		t.Errorf("notices = %v/%v/%v known=%v", saved.Verified, saved.Scam, saved.Fake, saved.NoticesKnown)
	}
	if !saved.PermissionsKnown || !saved.Permissions.Read || saved.Permissions.New {
		t.Errorf("permissions = %+v known=%v", saved.Permissions, saved.PermissionsKnown)
	}
	// This double replaces the whole record, as its map says; the repository's
	// own preservation rule is covered in internal/channelstore.
	if saved.AvatarPhotoID != 9 {
		t.Errorf("avatar photo = %d", saved.AvatarPhotoID)
	}
	if saved.ParticipantsCount == nil || *saved.ParticipantsCount != 42 {
		t.Errorf("participants = %v", saved.ParticipantsCount)
	}
}

func TestJoinRecordsAnAlreadyMemberChannel(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.join = rpc.JoinResult{
		Channel: rpc.ChannelInfo{ID: "500", Title: "Example", IsMember: true, MembershipStatus: "member"},
		Outcome: rpc.OutcomeAlreadyMember,
	}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Result string `json:"result"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	if body.Data.Result != "already_member_added_to_management" {
		t.Fatalf("result = %q", body.Data.Result)
	}
	if store.upserts != 1 {
		t.Fatalf("an already-member channel must still be recorded; upserts = %d", store.upserts)
	}
}

func TestRepeatedJoinIsIdempotent(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.join = rpc.JoinResult{
		Channel: rpc.ChannelInfo{ID: "500", Title: "Example", IsMember: true, MembershipStatus: "member"},
		Outcome: rpc.OutcomeAlreadyMember,
	}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	for i := 0; i < 3; i++ {
		resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d status = %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}
	if len(store.channels) != 1 {
		t.Fatalf("records = %d, want 1", len(store.channels))
	}

	// The last call reports the channel was already managed.
	resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	var body struct {
		Data struct {
			Result string `json:"result"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.Data.Result != "already_managed" {
		t.Fatalf("result = %q", body.Data.Result)
	}
}

func TestAPendingJoinIsNotStoredAsMember(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.join = rpc.JoinResult{
		Channel: rpc.ChannelInfo{
			ID: "500", Title: "Example", IsMember: false, MembershipStatus: "join_pending",
		},
		Outcome: rpc.OutcomePending,
	}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Channel struct {
				IsMember         bool   `json:"is_member"`
				MembershipStatus string `json:"membership_status"`
			} `json:"channel"`
			Result string `json:"result"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	if body.Data.Result != "join_pending" {
		t.Fatalf("result = %q", body.Data.Result)
	}
	if body.Data.Channel.IsMember || body.Data.Channel.MembershipStatus != "join_pending" {
		t.Fatalf("channel = %+v", body.Data.Channel)
	}
	saved := store.channels[recordKey("official", 500)]
	if saved.MembershipStatus != "join_pending" {
		t.Fatalf("stored status = %q", saved.MembershipStatus)
	}
}

func TestAFailedJoinCreatesNoRecord(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.joinE = rpc.NewError(rpc.CodeRateLimited, "slow down", 30)
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if len(store.channels) != 0 {
		t.Fatalf("a failed join recorded %d channels", len(store.channels))
	}
}

func TestJoinAfterConfirmedMembershipWithAFailedWriteIsRecoverable(t *testing.T) {
	store := newStore()
	store.err = errors.New("disk is full")
	worker := readyWorker()
	worker.join = rpc.JoinResult{
		Channel: rpc.ChannelInfo{ID: "500", Title: "Example", IsMember: true, MembershipStatus: "member"},
		Outcome: rpc.OutcomeJoined,
	}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var body errorEnvelope
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if !strings.Contains(body.Error.Message, "retry") {
		t.Fatalf("message must say the retry is safe: %q", body.Error.Message)
	}

	// Retrying after the storage recovers completes the persistence.
	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()
	resp = env.do(t, http.MethodPost, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.channels) != 1 {
		t.Fatalf("retry recorded %d channels", len(store.channels))
	}
}

func TestJoinRequiresAPOST(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = newStore() })

	resp := env.get(t, "/api/v1/telegram/official/channels/500/join")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET join = %d, want 405", resp.StatusCode)
	}
	resp.Body.Close()
	if worker.callCount() != 0 {
		t.Fatal("a GET must not reach the worker")
	}
}

func TestJoinRejectsAnInvalidReference(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = newStore() })

	for _, path := range []string{
		"/api/v1/telegram/official/channels/abc/join",
		"/api/v1/telegram/official/channels/0/join",
	} {
		resp := env.do(t, http.MethodPost, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestListNeverReadsTheLocalStore(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.channels = rpc.ChannelPage{Channels: []rpc.Channel{{ID: 500, Title: "From Telegram"}}}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.get(t, "/api/v1/telegram/official/channels?page=1&per_page=20")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	// The global view admits every channel, and a channel with no local row has
	// no assignment to check, so the listing is answered without reading the
	// store. (A caller restricted to per-channel grants must resolve each
	// dialog; that is covered in perms_test.go.)
	if store.managedIDsCalls != 0 || store.gets != 0 || store.upserts != 0 {
		t.Fatalf("listing touched the local store: %+v", store)
	}
}

func TestSearchDoesNotInsertLocalRecords(t *testing.T) {
	store := newStore()
	worker := readyWorker()
	worker.find = rpc.FindResult{Channels: []rpc.ChannelInfo{{ID: "500", Title: "Example"}}}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.get(t, "/api/v1/telegram/official/channels/find?q=example")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if store.upserts != 0 {
		t.Fatalf("search inserted %d records", store.upserts)
	}
}

func TestFindMarksAManagedChannel(t *testing.T) {
	store := newStore()
	if _, err := store.Upsert(context.Background(), ManagedRecord{
		ClientKey: "official", ChannelID: 500, PeerType: "channel", MembershipStatus: "member",
	}); err != nil {
		t.Fatal(err)
	}
	worker := readyWorker()
	worker.find = rpc.FindResult{Channels: []rpc.ChannelInfo{
		{ID: "500", Title: "Managed one", IsMember: true, MembershipStatus: "member"},
		{ID: "501", Title: "Unmanaged one", IsMember: true, MembershipStatus: "member"},
	}}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.get(t, "/api/v1/telegram/official/channels/find?q=example")
	var body findBody
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	if len(body.Data.Channels) != 2 {
		t.Fatalf("channels = %+v", body.Data.Channels)
	}
	if !body.Data.Channels[0].IsManaged {
		t.Error("the stored channel must report is_managed")
	}
	if body.Data.Channels[1].IsManaged {
		t.Error("an unstored channel must not report is_managed")
	}
	// Membership and management are independent flags.
	if !body.Data.Channels[1].IsMember {
		t.Error("a member channel that is not managed still reports is_member")
	}
}

func TestAvatarServesBytesFromTheCache(t *testing.T) {
	var calls int32
	cache, err := avatarcache.Open(filepath.Join(t.TempDir(), "cache"), avatarcache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	worker := readyWorker()
	worker.avatar = rpc.AvatarFile{MimeType: "image/jpeg", PhotoID: 9, Data: []byte("jpeg-bytes")}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Avatars = cache
	})

	for i := 0; i < 2; i++ {
		resp := env.get(t, "/api/v1/telegram/official/channels/500/avatar")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "jpeg-bytes" {
			t.Fatalf("body = %q", body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
			t.Fatalf("content type = %q", ct)
		}
		if cc := resp.Header.Get("Cache-Control"); cc == "" {
			t.Fatal("a cache header is required")
		}
		if etag := resp.Header.Get("ETag"); etag != `"9"` {
			t.Fatalf("etag = %q", etag)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("unexpected fetch count %d", got)
	}
	if worker.callCount() != 1 {
		t.Fatalf("worker calls = %d; the second request must come from the cache", worker.callCount())
	}
}

func TestAvatarWithoutAPhotoIs404(t *testing.T) {
	cache, err := avatarcache.Open(filepath.Join(t.TempDir(), "cache"), avatarcache.Options{})
	if err != nil {
		t.Fatal(err)
	}
	worker := readyWorker()
	worker.avatarE = rpc.NewError(rpc.CodeNoAvatar, "none", 0)
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Avatars = cache })

	resp := env.get(t, "/api/v1/telegram/official/channels/500/avatar")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body errorEnvelope
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.Error.Code != gateway.CodeNotFound {
		t.Fatalf("code = %q", body.Error.Code)
	}
}

func TestAvatarRejectsAnInviteReference(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/+AbCdEf123456/avatar")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if worker.callCount() != 0 {
		t.Fatal("an invite reference must not reach the worker")
	}
}

func TestAvatarServesDirectlyWithoutACache(t *testing.T) {
	worker := readyWorker()
	worker.avatar = rpc.AvatarFile{MimeType: "image/png", Data: []byte("png")}
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/500/avatar")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "png" {
		t.Fatalf("body = %q", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestChannelListingCarriesPermissions(t *testing.T) {
	worker := readyWorker()
	worker.channels = rpc.ChannelPage{Channels: []rpc.Channel{
		{ID: 500, Title: "Example", Kind: "channel"},
	}}
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	channels := parsed["data"].(map[string]any)["channels"].([]any)
	perms, ok := channels[0].(map[string]any)["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("listing has no permissions object: %s", raw)
	}
	for _, field := range []string{"new", "edit", "delete", "read"} {
		if _, ok := perms[field].(bool); !ok {
			t.Fatalf("permissions.%s = %v, want a bool", field, perms[field])
		}
	}
}

func TestSingleChannelReturnsTheTelegramState(t *testing.T) {
	worker := readyWorker()
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "500", Type: "channel", Title: "Example", Username: "example",
		Description:      "about",
		AvatarAvailable:  true,
		IsMember:         true,
		MembershipStatus: "member",
		Permissions:      rpc.Permissions{Read: true},
	}}
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/500")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Channel struct {
				ID          string `json:"id"`
				Title       string `json:"title"`
				Description string `json:"description"`
				AvatarURL   string `json:"avatar_url"`
				IsMember    bool   `json:"is_member"`
				IsManaged   bool   `json:"is_managed"`
				Permissions struct {
					Read *bool `json:"read"`
				} `json:"permissions"`
			} `json:"channel"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	ch := body.Data.Channel
	if ch.ID != "500" || ch.Title != "Example" || ch.Description != "about" {
		t.Fatalf("channel = %+v", ch)
	}
	if !ch.IsMember || ch.IsManaged {
		t.Fatalf("membership/management = %+v", ch)
	}
	if ch.AvatarURL != "/api/v1/telegram/official/channels/500/avatar" {
		t.Fatalf("avatar url = %q", ch.AvatarURL)
	}
	if ch.Permissions.Read == nil || !*ch.Permissions.Read {
		t.Fatalf("permissions = %+v", ch.Permissions)
	}
	if worker.lastRef.ChannelID != 500 {
		t.Fatalf("worker saw %+v", worker.lastRef)
	}
}

func TestSingleChannelReportsTheLocalSelection(t *testing.T) {
	store := newStore()
	if _, err := store.Upsert(context.Background(), ManagedRecord{
		ClientKey: "official", ChannelID: 500, PeerType: "channel", MembershipStatus: "member",
	}); err != nil {
		t.Fatal(err)
	}
	worker := readyWorker()
	worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "500", Title: "Example", IsMember: true, MembershipStatus: "member",
	}}
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) { c.Channels = store })

	resp := env.get(t, "/api/v1/telegram/official/channels/500")
	var body struct {
		Data struct {
			Channel struct {
				IsManaged bool `json:"is_managed"`
			} `json:"channel"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if !body.Data.Channel.IsManaged {
		t.Fatal("a recorded channel must report is_managed")
	}
}

func TestSingleChannelRejectsABadReference(t *testing.T) {
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	if worker.callCount() != 0 {
		t.Fatal("a malformed id must not reach the worker")
	}
}

func TestSingleChannelMapsNotFound(t *testing.T) {
	worker := readyWorker()
	worker.channelE = rpc.NewError(rpc.CodeChannelNotFound, "nope", 0)
	env := newEnv(t, registry{"official": worker})

	resp := env.get(t, "/api/v1/telegram/official/channels/500")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body errorEnvelope
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.Error.Code != gateway.CodeChannelNotFound {
		t.Fatalf("code = %q", body.Error.Code)
	}
}
