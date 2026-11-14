package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/accountauth"
	"github.com/mirza/telesync/internal/accountid"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// These tests are the v0.0.5 two-user isolation matrix (brief §37), run over
// direct HTTP requests against a real SQLite store and the real gateway.
//
// Two users, A and B, each hold their own Telegram account on the same server
// definition. Every row below is an attack one could make with a valid
// credential and a guessed or copied identifier. Nothing here asserts on a
// struct field: each case is a request and the answer it gets.

// accountUser is one user with their own Telegram account and the worker that
// serves it.
type accountUser struct {
	identity
	account channelstore.Account
	worker  *fakeWorker
}

// accountsEnv is the two-user matrix's world: one store, one gateway, two users
// each holding exactly one account on the same server key.
type accountsEnv struct {
	*accountEnv
	a, b    accountUser
	channel channelstore.Channel // B's managed channel, local id known to both
}

// newAccountsEnv builds the matrix's world.
//
// Both users hold the same global permissions and the same per-channel grants,
// so no difference in the answer can come from the permission rows: every
// refusal below is account ownership, and nothing else.
func newAccountsEnv(t *testing.T) *accountsEnv {
	t.Helper()
	env := newAccountEnv(t, registry{})
	env.signedIn = true

	out := &accountsEnv{accountEnv: env}
	out.a = out.ownAccount(t, "alice", env.defaults)
	out.b = out.ownAccount(t, "bob")

	ctx := context.Background()
	out.channel = out.ownChannel(t, out.b, 5001)
	// A's own channel, so a denial for B's channel is not "A has no channels".
	out.ownChannel(t, out.a, 5002)

	// The same grants for both: view on every channel of their own.
	for _, who := range []accountUser{out.a, out.b} {
		out.grantChannels(t, who.identity, allow(out.channel.ID))
		if err := out.store.ReplacePermissions(ctx, who.user.ID, channelstore.PermissionSet{
			Global:   channelstore.GlobalPermissions(),
			Channels: []channelstore.ResourcePermissions{allow(out.channel.ID)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// ownAccount creates a user with one Telegram account on the "official" server
// and a worker registered under the account id.
func (e *accountsEnv) ownAccount(t *testing.T, name string, existing ...identity) accountUser {
	t.Helper()
	ctx := context.Background()
	who := identity{}
	if len(existing) > 0 {
		who = existing[0]
	} else {
		who = e.account(t, name, channelstore.GlobalPermissions()...)
	}
	id, err := accountid.New()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := e.store.Create(ctx, channelstore.Account{
		ID:          id,
		OwnerUserID: who.user.ID,
		ServerKey:   "official",
		NetworkType: channelstore.NetworkTelegram,
		// One remote identity per user is all this fixture ever needs, and it
		// keeps the owner-scoped uniqueness honest: a second account on the same
		// server is a second record, not a second registration of the same
		// remote identity.
		RemoteUserID:         1000 + int64(len(e.workers)),
		RemoteUsername:       name,
		Status:               channelstore.AccountStatusConnected,
		SessionEncKeyVersion: 1,
	})
	if err != nil {
		t.Fatalf("Create account: %v", err)
	}
	w := readyWorker()
	w.health.Key = id
	w.profile.Key = id
	e.workers[id] = w
	return accountUser{identity: who, account: rec, worker: w}
}

// ownChannel registers one managed channel under a user's own account.
func (e *accountsEnv) ownChannel(t *testing.T, who accountUser, telegramID int64) channelstore.Channel {
	t.Helper()
	saved, err := e.store.RegisterForAccount(context.Background(), channelstore.Channel{
		ClientKey:        "official",
		AccountID:        who.account.ID,
		ChannelID:        telegramID,
		PeerType:         channelstore.PeerChannel,
		Title:            fmt.Sprintf("Channel %d", telegramID),
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatalf("RegisterForAccount: %v", err)
	}
	return saved
}

// body is a JSON object body for a request.
func body(fields map[string]any) string {
	raw, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// wantCode asserts one error code at one status, and returns the response.
func (e *accountsEnv) wantCode(t *testing.T, who identity, method, path, payload string,
	status int, code string) *http.Response {

	t.Helper()
	resp := e.send(t, who.token, method, path, payload)
	if resp.StatusCode != status {
		resp.Body.Close()
		t.Fatalf("%s %s: status = %d, want %d", method, path, resp.StatusCode, status)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		resp.Body.Close()
		t.Fatalf("%s %s: decode: %v", method, path, err)
	}
	resp.Body.Close()
	if env.Error.Code != code {
		t.Fatalf("%s %s: code = %q, want %q", method, path, env.Error.Code, code)
	}
	return resp
}

// wantOK asserts a 2xx and returns the raw body.
func (e *accountsEnv) wantOK(t *testing.T, who identity, method, path, payload string) string {
	t.Helper()
	resp := e.send(t, who.token, method, path, payload)
	raw := readAll(t, resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		t.Fatalf("%s %s: status = %d, body = %s", method, path, resp.StatusCode, raw)
	}
	return raw
}

// --- the account routes -----------------------------------------------------

func TestEachUserListsOnlyTheirOwnTelegramAccounts(t *testing.T) {
	env := newAccountsEnv(t)
	ctx := context.Background()

	aRaw := env.wantOK(t, env.a.identity, http.MethodGet, "/api/v1/me/telegram-accounts", "")
	ids := accountIDsOf(t, aRaw)
	if len(ids) != 1 || ids[0] != env.a.account.ID {
		t.Fatalf("A's accounts = %v, want only %s", ids, env.a.account.ID)
	}

	bRaw := env.wantOK(t, env.b.identity, http.MethodGet, "/api/v1/me/telegram-accounts", "")
	if ids := accountIDsOf(t, bRaw); len(ids) != 1 || ids[0] != env.b.account.ID {
		t.Fatalf("B's accounts = %v, want only %s", ids, env.b.account.ID)
	}
	_ = ctx
}

func TestAccountListingUsesTheLiveRunnerStatus(t *testing.T) {
	env := newAccountsEnv(t)
	cfg := env.handlerConfig()
	cfg.AccountStatus = func(_ context.Context, account channelstore.Account) string {
		if account.ID == env.a.account.ID {
			return channelstore.AccountStatusReconnecting
		}
		return ""
	}
	env.useConfig(cfg)

	raw := env.wantOK(t, env.a.identity, http.MethodGet, "/api/v1/me/telegram-accounts", "")
	var response struct {
		Data struct {
			Accounts []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Accounts) != 1 ||
		response.Data.Accounts[0].ID != env.a.account.ID ||
		response.Data.Accounts[0].Status != channelstore.AccountStatusReconnecting {
		t.Fatalf("account response = %+v, want the live Runner status", response.Data.Accounts)
	}
}

func TestOneUserCannotReadAnotherUsersAccountByID(t *testing.T) {
	env := newAccountsEnv(t)
	// A guesses B's account id. The answer is not-found, never forbidden: a
	// 403 would confirm the id exists and turn this route into an oracle.
	env.wantCode(t, env.a.identity, http.MethodGet,
		"/api/v1/me/telegram-accounts/"+env.b.account.ID, "", http.StatusNotFound, gateway.CodeNotFound)
}

func TestOneUserCannotDisconnectOrDeleteAnotherUsersAccount(t *testing.T) {
	env := newAccountsEnv(t)
	for _, call := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/me/telegram-accounts/" + env.b.account.ID + "/disconnect"},
		{http.MethodPost, "/api/v1/me/telegram-accounts/" + env.b.account.ID + "/logout"},
		{http.MethodDelete, "/api/v1/me/telegram-accounts/" + env.b.account.ID},
	} {
		env.wantCode(t, env.a.identity, call.method, call.path, "", http.StatusNotFound, gateway.CodeNotFound)
	}
	// B's account is untouched.
	rec, err := env.store.ByID(context.Background(), env.b.user.ID, env.b.account.ID)
	if err != nil {
		t.Fatalf("B's account vanished: %v", err)
	}
	if rec.Status != channelstore.AccountStatusConnected {
		t.Fatalf("B's account status = %q, want it untouched", rec.Status)
	}
}

func TestOneUserCannotCancelAnotherUsersLoginAttempt(t *testing.T) {
	env := newAccountsEnv(t)
	const foreign = "lga_00000000000000000000000000"
	for _, call := range []struct{ method, path, payload string }{
		{http.MethodGet, "/api/v1/me/telegram-accounts/login/" + foreign, ""},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/" + foreign + "/code",
			body(map[string]any{"code": "12345"})},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/" + foreign + "/password",
			body(map[string]any{"password": "secret"})},
		{http.MethodPost, "/api/v1/me/telegram-accounts/login/" + foreign + "/cancel", ""},
	} {
		// An attempt that does not exist for this owner is not found, exactly
		// like one that belongs to someone else: no owner oracle.
		env.wantCode(t, env.a.identity, call.method, call.path, call.payload,
			http.StatusNotFound, "login_attempt_invalid")
	}
}

// --- the account-scoped resource family ------------------------------------

func TestAccountScopedChannelRoutesAnswerForTheOwnersAccount(t *testing.T) {
	env := newAccountsEnv(t)
	// The worker that serves B's account answers for B's account. Registering
	// it under the account id is what the supervisor does for a real one.
	env.workers[env.b.account.ID] = env.b.worker
	raw := env.wantOK(t, env.b.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.b.account.ID+"/channels/"+strconv.FormatInt(env.channel.ChannelID, 10)+"/messages", "")
	if !strings.Contains(raw, "messages") {
		t.Fatalf("body = %s", raw)
	}
}

func TestConnectingAccountCannotRouteTelegramOperations(t *testing.T) {
	env := newAccountsEnv(t)
	pendingID, err := accountid.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.Create(context.Background(), channelstore.Account{
		ID: pendingID, OwnerUserID: env.a.user.ID, ServerKey: "official",
		NetworkType: channelstore.NetworkTelegram, Status: channelstore.AccountStatusConnecting,
	}); err != nil {
		t.Fatalf("Create pending account: %v", err)
	}
	// Even if a worker is accidentally registered during a startup race, the
	// pending identity must not be used until Telegram confirms authorization.
	env.workers[pendingID] = env.a.worker
	path := "/api/v1/telegram/accounts/" + pendingID + "/channels/1"
	env.wantCode(t, env.a.identity, http.MethodGet, path, "",
		http.StatusServiceUnavailable, "account_not_connected")
}

func TestOwnedAccountWithoutWorkerRegistryIsUnavailable(t *testing.T) {
	env := newAccountsEnv(t)
	cfg := env.handlerConfig()
	cfg.Workers = nil
	env.useConfig(cfg)

	env.wantCode(t, env.a.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.a.account.ID+"/channels/5002",
		"", http.StatusServiceUnavailable, gateway.CodeAccountNotConnected)
}

func TestAccountScopedMessageMediaURLKeepsTheAccountRouteSelector(t *testing.T) {
	env := newAccountsEnv(t)
	env.b.worker.messages = rpc.MessagePage{
		Messages: []rpc.Message{{
			ID:    12,
			Media: &rpc.Media{ID: "1:1", Type: "photo", MimeType: "image/jpeg", Size: 8},
		}},
	}
	path := "/api/v1/telegram/accounts/" + env.b.account.ID +
		"/channels/" + strconv.FormatInt(env.channel.ChannelID, 10) + "/messages"
	raw := env.wantOK(t, env.b.identity, http.MethodGet, path, "")
	var response struct {
		Data struct {
			Messages []struct {
				Media struct {
					URL string `json:"url"`
				} `json:"media"`
			} `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Messages) != 1 {
		t.Fatalf("messages = %s", raw)
	}
	want := "/telegram/accounts/" + env.b.account.ID +
		"/channels/" + strconv.FormatInt(env.channel.ChannelID, 10) + "/messages/12/media"
	if got := response.Data.Messages[0].Media.URL; got != want {
		t.Fatalf("media URL = %q, want account-scoped route %q", got, want)
	}
}

func TestOneUserCannotReadMessagesThroughAnotherUsersAccount(t *testing.T) {
	env := newAccountsEnv(t)
	// A names their own channel id through B's account.
	env.wantCode(t, env.a.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.b.account.ID+"/channels/"+strconv.FormatInt(env.channel.ChannelID, 10)+"/messages",
		"", http.StatusNotFound, gateway.CodeNotFound)
}

func TestOneUserCannotSendEditOrDeleteThroughAnotherUsersAccount(t *testing.T) {
	env := newAccountsEnv(t)
	const channelID = "5001"
	base := "/api/v1/telegram/accounts/" + env.b.account.ID + "/channels/" + channelID
	calls := []struct{ method, path, payload string }{
		{http.MethodPost, base + "/messages/send", body(map[string]any{"text": "hello"})},
		{http.MethodPut, base + "/messages/12", body(map[string]any{"text": "edited"})},
		{http.MethodDelete, base + "/messages/12", ""},
	}
	for _, call := range calls {
		env.wantCode(t, env.a.identity, call.method, call.path, call.payload,
			http.StatusNotFound, gateway.CodeNotFound)
	}
	if env.a.worker.callCount() != 0 || env.b.worker.callCount() != 0 {
		t.Fatalf("a refused request reached a worker: A=%d B=%d",
			env.a.worker.callCount(), env.b.worker.callCount())
	}
}

func TestOneUserCannotReadStreamMediaThroughAnotherUsersAccount(t *testing.T) {
	env := newAccountsEnv(t)
	env.wantCode(t, env.a.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.b.account.ID+"/channels/5001/messages/12/media", "",
		http.StatusNotFound, gateway.CodeNotFound)
}

func TestAccountScopedMessageAndMediaRequireRegistrationUnderNamedAccount(t *testing.T) {
	env := newAccountsEnv(t)
	// A has global channel visibility, but B's registration is not A's
	// registration. Use B's local row id as the Telegram id to also pin that
	// the two numeric namespaces cannot collide into an authorization grant.
	collisionID := env.channel.ID
	if _, found, err := env.store.GetForAccount(context.Background(), env.a.account.ID, collisionID); err != nil || found {
		t.Fatalf("collision channel already registered under A: found=%v err=%v", found, err)
	}
	env.workers[env.a.account.ID] = env.a.worker
	for _, path := range []string{
		"/api/v1/telegram/accounts/" + env.a.account.ID + "/channels/" + strconv.FormatInt(collisionID, 10) + "/messages",
		"/api/v1/telegram/accounts/" + env.a.account.ID + "/channels/" + strconv.FormatInt(collisionID, 10) + "/messages/12/media",
	} {
		env.wantCode(t, env.a.identity, http.MethodGet, path, "", http.StatusNotFound, gateway.CodeNotFound)
	}
	if got := env.a.worker.callCount(); got != 0 {
		t.Fatalf("unregistered channel reached A's worker %d times", got)
	}
	if got := env.b.worker.callCount(); got != 0 {
		t.Fatalf("unregistered channel reached B's worker %d times", got)
	}
}

func TestAccountScopedMessageRejectsSameOwnersOtherAccountRegistration(t *testing.T) {
	env := newAccountsEnv(t)
	second := env.ownAccount(t, "alice-second", env.a.identity)
	channel := env.ownChannel(t, second, 7001)
	firstWorker := readyWorker()
	env.workers[env.a.account.ID] = firstWorker

	path := "/api/v1/telegram/accounts/" + env.a.account.ID + "/channels/" +
		strconv.FormatInt(channel.ChannelID, 10) + "/messages"
	env.wantCode(t, env.a.identity, http.MethodGet, path, "", http.StatusNotFound, gateway.CodeNotFound)
	if got := firstWorker.callCount(); got != 0 {
		t.Fatalf("channel registered under the other account reached the named account worker %d times", got)
	}
	if got := env.a.worker.callCount(); got != 0 {
		t.Fatalf("channel request reached an unrelated worker %d times", got)
	}
}

type loginStartRecorder struct {
	calls    int
	owner    int64
	req      accountauth.StartRequest
	startErr error
}

type authorizedLoginRecorder struct {
	attempt accountauth.LoginAttempt
}

func (*authorizedLoginRecorder) Start(context.Context, int64, accountauth.StartRequest) (accountauth.LoginAttempt, error) {
	return accountauth.LoginAttempt{}, nil
}
func (*authorizedLoginRecorder) Status(context.Context, int64, string) (accountauth.LoginAttempt, error) {
	return accountauth.LoginAttempt{}, nil
}
func (f *authorizedLoginRecorder) SubmitCode(context.Context, int64, string, string) (accountauth.LoginAttempt, error) {
	return f.attempt, nil
}
func (*authorizedLoginRecorder) SubmitPassword(context.Context, int64, string, string) (accountauth.LoginAttempt, error) {
	return accountauth.LoginAttempt{}, nil
}
func (*authorizedLoginRecorder) Cancel(context.Context, int64, string) error { return nil }

func (f *loginStartRecorder) Start(_ context.Context, owner int64, req accountauth.StartRequest) (accountauth.LoginAttempt, error) {
	f.calls++
	f.owner, f.req = owner, req
	if f.startErr != nil {
		return accountauth.LoginAttempt{}, f.startErr
	}
	return accountauth.LoginAttempt{ID: "lga_test", ServerKey: req.ServerKey, State: "created"}, nil
}
func (*loginStartRecorder) Status(context.Context, int64, string) (accountauth.LoginAttempt, error) {
	return accountauth.LoginAttempt{}, nil
}
func (*loginStartRecorder) SubmitCode(context.Context, int64, string, string) (accountauth.LoginAttempt, error) {
	return accountauth.LoginAttempt{}, nil
}
func (*loginStartRecorder) SubmitPassword(context.Context, int64, string, string) (accountauth.LoginAttempt, error) {
	return accountauth.LoginAttempt{}, nil
}
func (*loginStartRecorder) Cancel(context.Context, int64, string) error { return nil }

func TestLoginStartDispatchesStartInsteadOfAttemptStatus(t *testing.T) {
	env := newAccountsEnv(t)
	logins := &loginStartRecorder{}
	cfg := env.handlerConfig()
	cfg.Logins = logins
	cfg.Servers = []gateway.ServerInfo{{Key: "official", Type: "official", Enabled: true}}
	env.useConfig(cfg)

	resp := env.send(t, env.a.identity.token, http.MethodPost,
		"/api/v1/me/telegram-accounts/login/start",
		`{"server_key":"official","method":"phone","phone_number":"test-phone-redacted"}`)
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST login/start status=%d body=%s, want 201", resp.StatusCode, raw)
	}
	if logins.calls != 1 || logins.owner != env.a.user.ID ||
		logins.req.ServerKey != "official" || logins.req.NetworkType != channelstore.NetworkTelegram ||
		logins.req.Method != accountauth.MethodPhone || logins.req.Phone != "test-phone-redacted" {
		t.Fatalf("login start calls=%d owner=%d req=%+v", logins.calls, logins.owner, logins.req)
	}
}

func TestLoginStartAcceptsExactPhoneNumberPayloadWithBrowserHeaders(t *testing.T) {
	env := newAccountsEnv(t)
	logins := &loginStartRecorder{}
	cfg := env.handlerConfig()
	cfg.Logins = logins
	cfg.Servers = []gateway.ServerInfo{{Key: "cat", Type: "custom", Enabled: true}}
	cfg.BasePath = "/admin"
	env.useConfig(cfg)

	req := httptest.NewRequest(http.MethodPost,
		"/admin/api/v1/me/telegram-accounts/login/start",
		strings.NewReader(`{"server_key":"cat","method":"phone","phone_number":"test-phone-redacted"}`))
	req.Header.Set("Authorization", "Bearer "+env.a.identity.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", "http://localhost:8080")
	req.Header.Set("Referer", "http://localhost:8080/admin/my-accounts")
	req.AddCookie(&http.Cookie{Name: "panel_session", Value: "browser-test"})

	resp := env.doRaw(t, req)
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("browser-like POST login/start status=%d body=%s, want 201", resp.StatusCode, raw)
	}
	if strings.Contains(raw, "exactly one JSON object") {
		t.Fatalf("valid request was rejected as multiple JSON values: %s", raw)
	}
	if logins.calls != 1 || logins.owner != env.a.user.ID ||
		logins.req.ServerKey != "cat" || logins.req.NetworkType != channelstore.NetworkCustom ||
		logins.req.Method != accountauth.MethodPhone || logins.req.Phone != "test-phone-redacted" {
		t.Fatalf("login start calls=%d owner=%d req=%+v", logins.calls, logins.owner, logins.req)
	}
}

func TestLoginStartStrictlyDecodesOneJSONObject(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantStatus     int
		wantMessage    string
		wantLoginCalls int
	}{
		{
			name:        "multiple objects",
			body:        `{"server_key":"cat"}{"server_key":"official"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "exactly one JSON object",
		},
		{
			name:        "trailing invalid data",
			body:        `{"server_key":"cat"} trailing`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "exactly one JSON object",
		},
		{
			name:        "malformed JSON",
			body:        `{"server_key":`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "malformed JSON",
		},
		{
			name:        "empty body",
			body:        "",
			wantStatus:  http.StatusBadRequest,
			wantMessage: "body is required",
		},
		{
			name:        "array",
			body:        `[{"server_key":"cat"}]`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be a JSON object",
		},
		{
			name:        "null",
			body:        `null`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "must be a JSON object",
		},
		{
			name:        "invalid field type",
			body:        `{"server_key":[],"method":"phone","phone_number":"test-phone-redacted"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "invalid field type",
		},
		{
			name:        "unknown field",
			body:        `{"server_key":"cat","method":"phone","phone_number":"test-phone-redacted","extra":true}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "unknown field",
		},
		{
			name:        "body over the existing limit",
			body:        strings.Repeat(" ", (64<<10)+1),
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantMessage: "maximum allowed size",
		},
		{
			name:           "surrounding whitespace",
			body:           " \n\t{\"server_key\":\"cat\",\"method\":\"phone\",\"phone_number\":\"test-phone-redacted\"}\r\n ",
			wantStatus:     http.StatusCreated,
			wantLoginCalls: 1,
		},
		{
			name:           "matching legacy network type",
			body:           `{"server_key":"cat","network_type":"custom","method":"phone","phone_number":"test-phone-redacted"}`,
			wantStatus:     http.StatusCreated,
			wantLoginCalls: 1,
		},
		{
			name:        "missing server key",
			body:        `{"method":"phone","phone_number":"test-phone-redacted"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "server_key is required",
		},
		{
			name:        "missing phone number",
			body:        `{"server_key":"cat","method":"phone"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "phone_number is required",
		},
		{
			name:        "unconfigured server",
			body:        `{"server_key":"official","method":"phone","phone_number":"test-phone-redacted"}`,
			wantStatus:  http.StatusNotFound,
			wantMessage: "selected server is not configured",
		},
		{
			name:        "network type mismatch",
			body:        `{"server_key":"cat","network_type":"telegram","method":"phone","phone_number":"test-phone-redacted"}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "network_type must match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newAccountsEnv(t)
			logins := &loginStartRecorder{}
			cfg := env.handlerConfig()
			cfg.Logins = logins
			cfg.Servers = []gateway.ServerInfo{{Key: "cat", Type: "custom", Enabled: true}}
			env.useConfig(cfg)

			resp := env.sendBody(t, env.a.identity.token, http.MethodPost,
				"/api/v1/me/telegram-accounts/login/start", "application/json", tt.body)
			raw := readAll(t, resp)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("login/start status=%d body=%s, want %d", resp.StatusCode, raw, tt.wantStatus)
			}
			if tt.wantMessage != "" && !strings.Contains(raw, tt.wantMessage) {
				t.Fatalf("login/start body=%s, want message containing %q", raw, tt.wantMessage)
			}
			if logins.calls != tt.wantLoginCalls {
				t.Fatalf("login service called %d times, want %d (response %s)",
					logins.calls, tt.wantLoginCalls, raw)
			}
		})
	}
}

func TestLoginStartRejectsUnsupportedMethodAfterJSONDecode(t *testing.T) {
	env := newAccountsEnv(t)
	logins := &loginStartRecorder{startErr: accountauth.ErrUnsupportedMethod}
	cfg := env.handlerConfig()
	cfg.Logins = logins
	cfg.Servers = []gateway.ServerInfo{{Key: "cat", Type: "custom", Enabled: true}}
	env.useConfig(cfg)

	resp := env.send(t, env.a.identity.token, http.MethodPost,
		"/api/v1/me/telegram-accounts/login/start",
		`{"server_key":"cat","method":"sms","phone_number":"test-phone-redacted"}`)
	raw := readAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(raw, `"code":"unsupported_login_method"`) {
		t.Fatalf("unsupported method response status=%d body=%s", resp.StatusCode, raw)
	}
	if logins.calls != 1 || logins.req.ServerKey != "cat" ||
		logins.req.Method != "sms" || logins.req.Phone != "test-phone-redacted" {
		t.Fatalf("login service calls=%d req=%+v", logins.calls, logins.req)
	}
}

func TestAuthorizedLoginStartsTheAccountRunner(t *testing.T) {
	env := newAccountsEnv(t)
	logins := &authorizedLoginRecorder{attempt: accountauth.LoginAttempt{
		ID: "lga_authorized", AccountID: env.a.account.ID, ServerKey: "official",
		OwnerUserID: env.a.user.ID, State: accountauth.StateAuthorized,
	}}
	called := 0
	var gotOwner int64
	var gotAttempt accountauth.LoginAttempt
	cfg := env.handlerConfig()
	cfg.Logins = logins
	cfg.AccountAuthorized = func(_ context.Context, owner int64, attempt accountauth.LoginAttempt) {
		called++
		gotOwner, gotAttempt = owner, attempt
	}
	env.useConfig(cfg)

	raw := env.wantOK(t, env.a.identity, http.MethodPost,
		"/api/v1/me/telegram-accounts/login/lga_authorized/code",
		body(map[string]any{"code": "12345"}))
	if called != 1 || gotOwner != env.a.user.ID || gotAttempt.AccountID != env.a.account.ID ||
		!strings.Contains(raw, `"state":"authorized"`) {
		t.Fatalf("authorized completion callback called=%d owner=%d attempt=%+v response=%s",
			called, gotOwner, gotAttempt, raw)
	}
}

func TestAccountLifecycleStopsRunnerBeforeLogoutAndClearsSession(t *testing.T) {
	env := newAccountsEnv(t)
	var calls []string
	cfg := env.handlerConfig()
	cfg.StopAccount = func(_ context.Context, owner int64, accountID string) error {
		if owner != env.a.user.ID || accountID != env.a.account.ID {
			t.Errorf("StopAccount(owner=%d, id=%q)", owner, accountID)
		}
		calls = append(calls, "stop")
		return nil
	}
	cfg.ClearAccountSession = func(_ context.Context, owner int64, account channelstore.Account) error {
		if owner != env.a.user.ID || account.ID != env.a.account.ID {
			t.Errorf("ClearAccountSession(owner=%d, account=%+v)", owner, account)
		}
		if account.Status != channelstore.AccountStatusAuthenticationRequired {
			t.Errorf("session cleanup saw status %q, want logout to be persisted first", account.Status)
		}
		calls = append(calls, "clear")
		return nil
	}
	env.useConfig(cfg)

	raw := env.wantOK(t, env.a.identity, http.MethodPost,
		"/api/v1/me/telegram-accounts/"+env.a.account.ID+"/logout", "")
	if !strings.Contains(raw, `"status":"authentication_required"`) {
		t.Fatalf("logout response = %s", raw)
	}
	if strings.Join(calls, ",") != "stop,clear" {
		t.Fatalf("lifecycle calls = %v, want stop then clear", calls)
	}
}

// --- ID guessing ------------------------------------------------------------

func TestGuessedAccountIDsAreNotFoundAndDoNotLeakExistence(t *testing.T) {
	env := newAccountsEnv(t)
	// A real account of B, a real account of A, and an id that was never
	// minted must answer identically to A.
	candidates := []string{
		env.b.account.ID,
		env.a.account.ID,
		"acc_00000000000000000000000000",
		"official",
		"legacy_official",
	}
	for _, id := range candidates {
		got := env.statusOf(t, env.a.identity, http.MethodGet, "/api/v1/me/telegram-accounts/"+id, "")
		want := http.StatusNotFound
		if id == env.a.account.ID {
			want = http.StatusOK
		}
		if got != want {
			t.Fatalf("GET account %s: status = %d, want %d", id, got, want)
		}
	}
}

func (e *accountsEnv) statusOf(t *testing.T, who identity, method, path, payload string) int {
	t.Helper()
	resp := e.send(t, who.token, method, path, payload)
	resp.Body.Close()
	return resp.StatusCode
}

// --- legacy keyed routes ---------------------------------------------------

func TestLegacyRouteResolvesToTheCallersOwnAccount(t *testing.T) {
	env := newAccountsEnv(t)
	raw := env.wantOK(t, env.b.identity, http.MethodGet, "/api/v1/telegram/official/channels", "")
	if !strings.Contains(raw, "channels") {
		t.Fatalf("body = %s", raw)
	}
}

func TestLegacyRouteAmbiguityIsAccountSelectionRequired(t *testing.T) {
	env := newAccountsEnv(t)
	// B holds a second account on the same server: the keyed route can no
	// longer decide which one to act as, and says so rather than guessing.
	second := env.ownAccount(t, "bob-second", env.b.identity)
	if second.account.ID == env.b.account.ID {
		t.Fatal("the second account must be a different record")
	}
	env.wantCode(t, env.b.identity, http.MethodGet, "/api/v1/telegram/official/channels", "",
		http.StatusBadRequest, "account_selection_required")
}

func TestLegacyRouteNeverResolvesToAnotherUsersAccount(t *testing.T) {
	env := newAccountsEnv(t)
	// Carol has no account; A and B do. The keyed route must not fall back to
	// either owner's account or to the quarantined legacy session.
	carol := env.account(t, "carol", channelstore.GlobalPermissions()...)
	env.wantCode(t, carol, http.MethodGet, "/api/v1/telegram/official/channels", "",
		http.StatusNotFound, "account_not_owned")
}

func TestAccountScopedChannelListIsRegisteredOnMountedAdminMux(t *testing.T) {
	env := newAccountsEnv(t)
	env.b.worker.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{
			{ID: 7001, Title: "Writable", Permissions: rpc.Permissions{New: true, Read: true}},
			{ID: 7002, Title: "Read only", Permissions: rpc.Permissions{Read: true}},
		},
		Page: rpc.PageInfo{Page: 1, PerPage: 100, Total: 2},
	}

	// Exercise the registered gateway handler and its /admin mount, not a
	// direct handler call. This is the URL used by the publish dialog.
	env.handler = gateway.New(gateway.Config{
		BasePath:        "/admin",
		Users:           env.store,
		Tokens:          env.tokens,
		Operator:        operatorToken,
		Channels:        env.store,
		Categories:      env.store,
		Publishes:       env.store,
		Accounts:        env.store,
		AccountChannels: env.store,
		Resolver:        gateway.NewStoreAccountResolver(env.store),
		Workers:         env.workers,
	})

	server := httptest.NewServer(env.handler)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet,
		server.URL+"/admin/api/v1/telegram/accounts/"+env.b.account.ID+
			"/channels?page=1&per_page=100&capability=send", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+env.b.token)
	resp, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("GET mounted account channel list: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mounted account channel list status = %d; want 200, body=%s",
			resp.StatusCode, readAll(t, resp))
	}
	var got struct {
		Data struct {
			Channels []rpc.Channel `json:"channels"`
		} `json:"data"`
		Meta struct {
			Page    int `json:"page"`
			PerPage int `json:"per_page"`
			Total   int `json:"total"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(got.Data.Channels) != 1 || got.Data.Channels[0].ID != 7001 {
		t.Fatalf("eligible channels = %+v; want only writable Telegram peer 7001", got.Data.Channels)
	}
	if got.Meta.Page != 1 || got.Meta.PerPage != 100 || got.Meta.Total != 2 {
		t.Fatalf("pagination metadata = %+v; want page=1 per_page=100 total=2", got.Meta)
	}
	if env.b.worker.lastPage != 1 || env.b.worker.lastPer != 100 {
		t.Fatalf("worker paging = %d/%d; want 1/100", env.b.worker.lastPage, env.b.worker.lastPer)
	}
}

func TestAccountScopedPublishUsesTheSelectedAccountAndUnregisteredPeer(t *testing.T) {
	env := newAccountsEnv(t)
	env.b.worker.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: env.channel.ChannelID, Title: "Source"},
		Messages: []rpc.Message{{ID: 9, Text: "source"}},
	}
	env.b.worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "7001", Type: "channel", Title: "Unregistered destination",
		IsMember: true, Permissions: rpc.Permissions{New: true, Read: true},
	}}
	env.b.worker.send = rpc.SendResult{
		Message:   &rpc.Message{ID: 10, Text: "edited"},
		ChannelID: "7001",
	}

	resp := env.send(t, env.b.token, http.MethodPost,
		"/api/v1/telegram/accounts/"+env.b.account.ID+"/messages/copy",
		strings.Replace(publishBody(env.channel.ID, 9, "edited"),
			`"channel_id": "555"`, `"channel_id": "7001"`, 1))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("account-scoped publish status = %d; want 200, body=%s",
			resp.StatusCode, readAll(t, resp))
	}
	if env.b.worker.sendCalls != 1 {
		t.Fatalf("selected account sends = %d; want 1", env.b.worker.sendCalls)
	}
	if env.a.worker.sendCalls != 0 {
		t.Fatalf("another account sends = %d; want 0", env.a.worker.sendCalls)
	}
	if env.b.worker.sentReq.ChannelID != 7001 || env.b.worker.sentReq.Text != "edited" {
		t.Fatalf("send request = %+v; want peer 7001 and prepared text", env.b.worker.sentReq)
	}
}

func TestAccountScopedPublishDoesNotResolveDestinationByServerKeyFirst(t *testing.T) {
	env := newAccountsEnv(t)
	const destinationID = int64(5002) // independently registered and accessible on A and B
	env.ownChannel(t, env.b, destinationID)
	env.b.worker.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: env.channel.ChannelID, Title: "Source"},
		Messages: []rpc.Message{{ID: 9, Text: "source"}},
	}
	env.b.worker.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{{
			ID: destinationID, Title: "B destination", Permissions: rpc.Permissions{New: true, Read: true},
		}},
		Page: rpc.PageInfo{Page: 1, PerPage: 100, Total: 1},
	}
	env.b.worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: strconv.FormatInt(destinationID, 10), Type: "channel", Title: "B destination",
		IsMember: true, Permissions: rpc.Permissions{New: true, Read: true},
	}}
	env.b.worker.send = rpc.SendResult{
		Message:   &rpc.Message{ID: 10, Text: "edited"},
		ChannelID: strconv.FormatInt(destinationID, 10),
	}

	listing := env.wantOK(t, env.b.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.b.account.ID+"/channels?capability=send", "")
	var discovered struct {
		Data struct {
			Channels []rpc.Channel `json:"channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(listing), &discovered); err != nil {
		t.Fatal(err)
	}
	if len(discovered.Data.Channels) != 1 || discovered.Data.Channels[0].ID != destinationID {
		t.Fatalf("send-capable destinations = %+v; want selected account peer %d",
			discovered.Data.Channels, destinationID)
	}

	path := "/api/v1/telegram/accounts/" + env.b.account.ID + "/messages/copy"
	payload := strings.Replace(publishBody(env.channel.ID, 9, "edited"),
		`"channel_id": "555"`, `"channel_id": "`+strconv.FormatInt(discovered.Data.Channels[0].ID, 10)+`"`, 1)
	headers := map[string]string{"Idempotency-Key": "account-scoped-copy-peer-5002"}
	resp := env.sendWith(t, env.b.token, http.MethodPost,
		path, headers, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("selected account publish status = %d; want 200, body=%s",
			resp.StatusCode, readAll(t, resp))
	}
	if env.b.worker.sendCalls != 1 || env.a.worker.sendCalls != 0 {
		t.Fatalf("send calls selected/other = %d/%d; want 1/0",
			env.b.worker.sendCalls, env.a.worker.sendCalls)
	}
	responseBody := readAll(t, resp)
	if !strings.Contains(responseBody, `"id":10`) ||
		!strings.Contains(responseBody, `"channel_id":"5002"`) {
		t.Fatalf("publish response = %s; want destination message 10 in channel 5002", responseBody)
	}
	replay := env.sendWith(t, env.b.token, http.MethodPost, path, headers, payload)
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("idempotent retry status = %d; want 200, body=%s",
			replay.StatusCode, readAll(t, replay))
	}
	replay.Body.Close()
	if env.b.worker.sendCalls != 1 {
		t.Fatalf("selected account sends after retry = %d; want 1", env.b.worker.sendCalls)
	}
}

func TestAccountScopedCopyResolvesSourceAndDestinationAccountsIndependently(t *testing.T) {
	env := newAccountsEnv(t)
	sourceAccount := env.ownAccount(t, "bob-source", env.b.identity)
	source := env.ownChannel(t, sourceAccount, 5003)
	sourceAccount.worker.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: source.ChannelID, Title: "Source on another account"},
		Messages: []rpc.Message{{ID: 9, Text: "source"}},
	}
	env.b.worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "7001", Type: "channel", Title: "Destination on selected account",
		IsMember: true, Permissions: rpc.Permissions{New: true, Read: true},
	}}
	env.b.worker.send = rpc.SendResult{
		Message:   &rpc.Message{ID: 10, Text: "edited"},
		ChannelID: "7001",
	}

	payload := strings.Replace(publishBody(source.ID, 9, "edited"),
		`"channel_id": "555"`, `"channel_id": "7001"`, 1)
	resp := env.send(t, env.b.token, http.MethodPost,
		"/api/v1/telegram/accounts/"+env.b.account.ID+"/messages/copy", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cross-account copy status = %d; want 200, body=%s",
			resp.StatusCode, readAll(t, resp))
	}
	resp.Body.Close()
	if sourceAccount.worker.lastMessageChannel != source.ChannelID ||
		sourceAccount.worker.lastMessageID != 9 {
		t.Fatalf("source lookup = %d/%d; want source account channel %d message 9",
			sourceAccount.worker.lastMessageChannel, sourceAccount.worker.lastMessageID,
			source.ChannelID)
	}
	if env.b.worker.sendCalls != 1 || sourceAccount.worker.sendCalls != 0 {
		t.Fatalf("send calls destination/source = %d/%d; want 1/0",
			env.b.worker.sendCalls, sourceAccount.worker.sendCalls)
	}
}

func TestAccountScopedIdempotencyKeysDoNotCollideAcrossOwnedAccounts(t *testing.T) {
	env := newAccountsEnv(t)
	second := env.ownAccount(t, "bob-second-destination", env.b.identity)
	env.b.worker.message = rpc.MessagePage{
		Channel:  rpc.Channel{ID: env.channel.ChannelID, Title: "Source"},
		Messages: []rpc.Message{{ID: 9, Text: "source"}},
	}
	for _, worker := range []*fakeWorker{env.b.worker, second.worker} {
		worker.channel = rpc.ChannelResult{Channel: rpc.ChannelInfo{
			ID: "7001", Type: "channel", Title: "Same peer, separate account",
			IsMember: true, Permissions: rpc.Permissions{New: true, Read: true},
		}}
		worker.send = rpc.SendResult{
			Message:   &rpc.Message{ID: 10, Text: "edited"},
			ChannelID: "7001",
		}
	}
	payload := strings.Replace(publishBody(env.channel.ID, 9, "edited"),
		`"channel_id": "555"`, `"channel_id": "7001"`, 1)
	headers := map[string]string{"Idempotency-Key": "same-client-key-on-two-accounts"}
	for _, account := range []accountUser{env.b, second} {
		resp := env.sendWith(t, env.b.token, http.MethodPost,
			"/api/v1/telegram/accounts/"+account.account.ID+"/messages/copy",
			headers, payload)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("publish via account %s status = %d; want 200, body=%s",
				account.account.ID, resp.StatusCode, readAll(t, resp))
		}
		resp.Body.Close()
	}
	if env.b.worker.sendCalls != 1 || second.worker.sendCalls != 1 {
		t.Fatalf("sends for account A/B = %d/%d; want 1/1",
			env.b.worker.sendCalls, second.worker.sendCalls)
	}
	for _, account := range []accountUser{env.b, second} {
		resp := env.sendWith(t, env.b.token, http.MethodPost,
			"/api/v1/telegram/accounts/"+account.account.ID+"/messages/copy",
			headers, payload)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("replay via account %s status = %d; want 200, body=%s",
				account.account.ID, resp.StatusCode, readAll(t, resp))
		}
		resp.Body.Close()
	}
	if env.b.worker.sendCalls != 1 || second.worker.sendCalls != 1 {
		t.Fatalf("sends after per-account replays = %d/%d; want 1/1",
			env.b.worker.sendCalls, second.worker.sendCalls)
	}
}

func TestAccountScopedChannelListReturnsSuccessfulEmptyPage(t *testing.T) {
	env := newAccountsEnv(t)
	env.a.worker.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{
			{ID: 8101, Title: "Read only", Permissions: rpc.Permissions{Read: true}},
		},
		Page: rpc.PageInfo{Page: 1, PerPage: 100, Total: 1},
	}
	raw := env.wantOK(t, env.a.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.a.account.ID+"/channels?page=1&per_page=100&capability=send", "")
	var got struct {
		Data struct {
			Channels []rpc.Channel `json:"channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Channels == nil || len(got.Data.Channels) != 0 {
		t.Fatalf("channels = %#v; want a successful empty collection", got.Data.Channels)
	}
}

func TestAccountScopedChannelListEnforcesOwnershipAndKeepsAccountsIndependent(t *testing.T) {
	env := newAccountsEnv(t)
	env.a.worker.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{{ID: 8101, Title: "A", Permissions: rpc.Permissions{New: true}}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 100, Total: 1},
	}
	env.b.worker.channels = rpc.ChannelPage{
		Channels: []rpc.Channel{{ID: 8201, Title: "B", Permissions: rpc.Permissions{New: true}}},
		Page:     rpc.PageInfo{Page: 1, PerPage: 100, Total: 1},
	}
	for _, tc := range []struct {
		who       identity
		account   channelstore.Account
		wantID    int64
		wantTitle string
	}{
		{env.a.identity, env.a.account, 8101, "A"},
		{env.b.identity, env.b.account, 8201, "B"},
	} {
		raw := env.wantOK(t, tc.who, http.MethodGet,
			"/api/v1/telegram/accounts/"+tc.account.ID+"/channels?capability=send", "")
		var got struct {
			Data struct {
				Channels []rpc.Channel `json:"channels"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data.Channels) != 1 || got.Data.Channels[0].ID != tc.wantID ||
			got.Data.Channels[0].Title != tc.wantTitle {
			t.Fatalf("account %s channels = %+v; want only %s/%d",
				tc.account.ID, got.Data.Channels, tc.wantTitle, tc.wantID)
		}
	}
	foreignPath := "/api/v1/telegram/accounts/" + env.b.account.ID + "/channels"
	env.wantCode(t, env.a.identity, http.MethodGet, foreignPath, "",
		http.StatusNotFound, gateway.CodeNotFound)
	if env.b.worker.calls != 1 {
		t.Fatalf("foreign request reached B worker; calls = %d", env.b.worker.calls)
	}
}

func TestDisconnectedAccountChannelListIsUnavailable(t *testing.T) {
	env := newAccountsEnv(t)
	env.a.worker.chanE = rpc.ErrNotConnected
	env.wantCode(t, env.a.identity, http.MethodGet,
		"/api/v1/telegram/accounts/"+env.a.account.ID+"/channels",
		"", http.StatusServiceUnavailable, gateway.CodeNotConnected)
	if env.a.worker.calls != 1 {
		t.Fatalf("disconnected account worker calls = %d; want one account-specific attempt", env.a.worker.calls)
	}
}

// --- quarantine -------------------------------------------------------------

func TestQuarantinedAccountsAreNotEligibleAndNotListed(t *testing.T) {
	env := newAccountsEnv(t)
	ctx := context.Background()
	// A channel joined through the legacy path lands on the quarantined
	// account of that server key (R8). That account must never be resolvable.
	if _, err := env.store.Upsert(ctx, channelstore.Channel{
		ClientKey: "official", ChannelID: 9001, PeerType: channelstore.PeerChannel,
		Title: "Legacy", MembershipStatus: channelstore.StatusMember,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	for _, who := range []identity{env.a.identity, env.b.identity} {
		raw := env.wantOK(t, who, http.MethodGet, "/api/v1/me/telegram-accounts", "")
		for _, id := range accountIDsOf(t, raw) {
			if strings.HasPrefix(id, "legacy_") {
				t.Fatalf("a quarantined account %s was listed to a user", id)
			}
		}
	}
	env.wantCode(t, env.a.identity, http.MethodGet,
		"/api/v1/me/telegram-accounts/legacy_official", "", http.StatusNotFound, gateway.CodeNotFound)
}

// --- shared access-assignment ----------------------------------------------

func TestSharedServerAccessAssignmentNoLongerGrantsATelegramAccount(t *testing.T) {
	env := newAccountsEnv(t)
	// The retired mechanism used to answer "which application user may use
	// which shared Telegram connection". Whatever remains of it, it must not
	// hand A a session: A reaches no Telegram account but their own.
	raw := env.wantOK(t, env.a.identity, http.MethodGet, "/api/v1/me/telegram-accounts", "")
	ids := accountIDsOf(t, raw)
	if len(ids) != 1 || ids[0] != env.a.account.ID {
		t.Fatalf("A's accounts = %v, want only their own", ids)
	}
}

// --- operator token ---------------------------------------------------------

func TestOperatorTokenHoldsNoPrivateTelegramAccount(t *testing.T) {
	env := newAccountsEnv(t)
	// The operator is a credential, not a user. It must not become the owner
	// of anyone's account or see their session-backed resources.
	resp := env.send(t, operatorToken, http.MethodGet, "/api/v1/me/telegram-accounts", "")
	raw := readAll(t, resp)
	if resp.StatusCode == http.StatusOK && strings.Contains(raw, env.b.account.ID) {
		t.Fatalf("the operator token listed another user's account: %s", raw)
	}
}

// accountIDsOf decodes the account listing.
func accountIDsOf(t *testing.T, raw string) []string {
	t.Helper()
	var body struct {
		Data struct {
			Accounts []struct {
				ID string `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	out := make([]string, 0, len(body.Data.Accounts))
	for _, a := range body.Data.Accounts {
		out = append(out, a.ID)
	}
	return out
}
