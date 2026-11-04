package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
)

// These helpers wire a real SQLite account store and the real token signer into
// the gateway, because the point of the tests that use them is that
// authorization reads the database on every request.

// identity is one test account.
type identity struct {
	user  channelstore.User
	token string
}

// accountEnv is a gateway over one SQLite store, with a token signer whose
// clock the test controls and a default signed-in caller.
type accountEnv struct {
	*testEnv
	store  *channelstore.Store
	tokens *auth.Tokens
	// defaults is the account the operator token stands in for. It holds every
	// permission, so the guard that keeps the last user administrator alive
	// never fires.
	defaults identity
	// signedIn is true once the suite's default credential is the default
	// caller's own JWT rather than the operator token.
	signedIn bool
}

// operatorToken is the fixed access token the test configuration gives the
// operator. It is a placeholder, never a real credential.
const operatorToken = "test-operator-token"

// operatorTokens accepts the literal test token before deferring to the real
// signer.
type operatorTokens struct{ real gateway.Tokens }

func (o operatorTokens) Issue(id int64) (string, time.Time, error) {
	if o.real == nil {
		return "", time.Time{}, auth.ErrTokenSubject
	}
	return o.real.Issue(id)
}

func (o operatorTokens) Parse(token string) (int64, error) {
	if token == letToken {
		return 1, nil
	}
	if o.real == nil {
		return 0, auth.ErrTokenSignature
	}
	return o.real.Parse(token)
}

const letToken = "test-session"

// newAccountEnv builds the gateway the way the daemon does, over a real store.
// mutate runs after the wiring, so a test can replace the account store.
func newAccountEnv(t *testing.T, workers registry, mutate ...func(*gateway.Config)) *accountEnv {
	t.Helper()
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "state.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// The signer uses the real clock, so "expired" in a test is genuinely
	// expired; the gateway's own timestamps stay fixed through Config.Now.
	tokens, err := auth.NewTokens(auth.TokenOptions{
		Secret: []byte(strings.Repeat("k", 48)),
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := &accountEnv{store: store, tokens: tokens}
	// The caller every request in this suite runs as: an account holding every
	// global permission. Resource rights are granted per test.

	env.defaults = env.account(t, "default-admin", channelstore.GlobalPermissions()...)
	env.testEnv = newEnv(t, workers, append([]func(*gateway.Config){func(c *gateway.Config) {
		c.Users = store
		c.Tokens = tokens
		c.Operator = operatorToken
		c.Channels = store
		c.Categories = store
		c.Publishes = store
	}}, mutate...)...)
	env.session = operatorToken
	return env
}

// account creates an active user with a password and returns its token and id.
func (e *accountEnv) account(t *testing.T, username string, globals ...channelstore.GlobalPermission) identity {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword("password-" + username)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.store.CreateUser(ctx, "User "+username, username, hash, true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := e.store.ReplacePermissions(ctx, u.ID, channelstore.PermissionSet{Global: globals}); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
	return identity{user: u, token: e.tokenFor(t, u.ID)}
}

func (e *accountEnv) tokenFor(t *testing.T, id int64) string {
	t.Helper()
	token, _, err := e.tokens.Issue(id)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// channelRow records a Telegram channel locally so it can carry an assignment.
func (e *accountEnv) channelRow(t *testing.T, key string, telegramID int64) channelstore.Channel {
	t.Helper()
	ch, err := e.store.Upsert(context.Background(), channelstore.Channel{
		ClientKey:        key,
		ChannelID:        telegramID,
		PeerType:         channelstore.PeerChannel,
		Title:            fmt.Sprintf("Channel %d", telegramID),
		MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// grantChannels replaces the channel assignments of one account.
func (e *accountEnv) grantChannels(t *testing.T, who identity, sets ...channelstore.ResourcePermissions) {
	t.Helper()
	ctx := context.Background()
	current, err := e.store.PermissionsOf(ctx, who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Channels = sets
	if err := e.store.ReplacePermissions(ctx, who.user.ID, current); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
}

// grantCategories replaces the category assignments of one account.
func (e *accountEnv) grantCategories(t *testing.T, who identity, sets ...channelstore.ResourcePermissions) {
	t.Helper()
	ctx := context.Background()
	current, err := e.store.PermissionsOf(ctx, who.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Categories = sets
	if err := e.store.ReplacePermissions(ctx, who.user.ID, current); err != nil {
		t.Fatalf("ReplacePermissions: %v", err)
	}
}

// defaultCaller is the send argument meaning "the suite's signed-in caller".
const defaultCaller = "<default>"

// send calls the API as who, or as the default caller when who is defaultCaller.
func (e *accountEnv) send(t *testing.T, who, method, path, body string) *http.Response {
	t.Helper()
	hreq := httptest.NewRequest(method, path, strings.NewReader(body))
	if who == defaultCaller {
		who = e.session
		if e.signedIn {
			who = e.defaults.token
		}
	}
	if who != "" {
		hreq.Header.Set("Authorization", "Bearer "+who)
	}
	if body != "" {
		hreq.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, hreq)
	return rec.Result()
}

// sendAnon is send for a caller with no credential at all: send reserves ""
// for the suite's default caller.
func (e *accountEnv) sendAnon(t *testing.T, method, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Result()
}

// leaveBody is a leave request body sent as the suite's signed-in caller.
//
// The route checks the per-channel delete right like any other, so a test that
// expects an allowed leave grants it first (grantDeleteOn).
func (e *accountEnv) leaveBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	if !e.signedIn {
		// The operator token has no account and no per-channel assignment;
		// the leave route refuses it by design.
		t.Fatal("the leave route needs an account; sign the suite in first (env.signedIn = true)")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// grantDeleteOn gives the suite's signed-in caller view and delete on one
// channel record, which is what the leave route requires.
func (e *accountEnv) grantDeleteOn(t *testing.T, ids ...int64) {
	t.Helper()
	sets := make([]channelstore.ResourcePermissions, 0, len(ids))
	for _, id := range ids {
		sets = append(sets, channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
		}})
	}
	e.grantChannels(t, e.defaults, sets...)
}

// sendBody is send with an explicit content type, so a test can send a body
// that declares none.
func (e *accountEnv) sendBody(t *testing.T, who, method, path, contentType, body string) *http.Response {
	t.Helper()
	hreq := httptest.NewRequest(method, path, strings.NewReader(body))
	if who == defaultCaller {
		who = e.session
		if e.signedIn {
			who = e.defaults.token
		}
	}
	if who != "" {
		hreq.Header.Set("Authorization", "Bearer "+who)
	}
	if contentType != "" {
		hreq.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, hreq)
	return rec.Result()
}

// doRaw runs one fully formed request through the handler.
func (e *accountEnv) doRaw(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// newRawRequest builds a request with no credential.
func newRawRequest(method, path, body string) *http.Request {
	return httptest.NewRequest(method, path, strings.NewReader(body))
}

// dialogsOpensWith registers a fake worker that lists the given dialogs, the
// way an account with them in its dialog list would see them.
func dialogsOpensWith(tgIDs ...int64) *fakeWorker {
	worker := readyWorker()
	for _, id := range tgIDs {
		worker.channels.Channels = append(worker.channels.Channels, rpc.Channel{ID: id, Title: fmt.Sprintf("channel %d", id)})
	}
	worker.channels.Page = rpc.PageInfo{Page: 1, PerPage: 20, Total: len(tgIDs)}
	return worker
}

// grantCategoryOnCreate makes every category the default caller creates
// through the API carry a full grant for that caller.
//
// The application has no "categories.update" global permission, so an
// administrator who creates a category is given view, update and delete on it:
// the creator's own access is what lets them manage what they just made. It is
// how the category contract tests keep being about the HTTP contract.
func (e *accountEnv) grantCategoryOnCreate() {
	ctx := context.Background()
	e.testEnv.installOnCreated(func(id int64) {
		value, err := e.store.PermissionsOf(ctx, e.defaults.user.ID)
		if err != nil {
			return
		}
		value.Categories = append(value.Categories, channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
			{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
			{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
		}})
		_ = e.store.ReplacePermissions(ctx, e.defaults.user.ID, value)
	})
}

// grantCategory gives the default caller full rights on one category created
// outside the API, mirroring grantCategoryOnCreate.
func (e *accountEnv) grantCategory(t *testing.T, id int64) {
	t.Helper()
	value, err := e.store.PermissionsOf(context.Background(), e.defaults.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	value.Categories = append(value.Categories, channelstore.ResourcePermissions{ResourceID: id, Permissions: []channelstore.ResourcePermission{
		{Action: channelstore.ActionView, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionUpdate, Effect: channelstore.EffectAllow},
		{Action: channelstore.ActionDelete, Effect: channelstore.EffectAllow},
	}})
	if err := e.store.ReplacePermissions(context.Background(), e.defaults.user.ID, value); err != nil {
		t.Fatal(err)
	}
}

// onlyCategoryPermissions reduces the default caller to "may create categories"
// plus whatever category grants the test adds, so a category list test sees
// exactly those grants.
func (e *accountEnv) onlyCategoryPermissions(t *testing.T) {
	t.Helper()
	if err := e.store.ReplacePermissions(context.Background(), e.defaults.user.ID, channelstore.PermissionSet{
		Global: []channelstore.GlobalPermission{channelstore.PermissionCategoriesCreate},
	}); err != nil {
		t.Fatal(err)
	}
}

// handlerConfig is the configuration the suite's handler was built from, for a
// test that needs a handler with one field changed.
func (e *accountEnv) handlerConfig() gateway.Config { return e.started }

// useConfig rebuilds the handler over the suite's store with cfg, and remembers
// it: handlerConfig answers with what the handler is built from now, so a test
// that changes one field and rebuilds keeps the rest of what it already set.
func (e *accountEnv) useConfig(cfg gateway.Config) {
	e.started = cfg
	e.handler = gateway.New(cfg)
}
