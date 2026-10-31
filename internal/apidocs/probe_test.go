package apidocs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mirza/telesync/internal/apidocs"
	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/avatarcache"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/wordpress"
)

// The probe is the gateway under a real SQLite store, populated with the
// accounts and records the documentation examples and snapshots need. It is
// the harness the coverage and snapshot tests drive; every credential it hands
// out is captured before the request it is used for.

// probeToken is the operator token the probe configures and sends. It is a
// placeholder, never a real credential. probePassword is the probe accounts'.
const (
	probeToken = "doc-site-token"
	// probePassword is what the documentation's login example shows.
	probePassword = "example-doc-password"
)

type probe struct {
	t       *testing.T
	handler http.Handler
	store   *channelstore.Store
	tokens  *auth.Tokens
}

// ServeHTTP lets a probe stand in wherever a plain handler is expected.
func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.handler.ServeHTTP(w, r) }

func newProbe(t *testing.T) *probe {
	t.Helper()
	p := &probe{t: t}
	p.handler = p.build()
	return p
}

func (p *probe) build() http.Handler {
	p.t.Helper()
	store, err := channelstore.Open(filepath.Join(p.t.TempDir(), "docs.db"), channelstore.Options{})
	if err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() { _ = store.Close() })
	p.store = store

	hash, err := auth.HashPassword(probePassword)
	if err != nil {
		p.t.Fatal(err)
	}
	ctx := context.Background()
	// "admin" is the account the documentation's login example uses; its
	// password is the one the example shows, so the documented login works.
	admin, err := store.CreateUser(ctx, "Documented Administrator", "admin", hash, true)
	if err != nil {
		p.t.Fatal(err)
	}
	limited, err := store.CreateUser(ctx, "Documented Limited User", "doc-limited", hash, true)
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := store.CreateUser(ctx, "Documented Other User", "doc-other", hash, true); err != nil {
		p.t.Fatal(err)
	}
	channel, err := store.Upsert(ctx, channelstore.Channel{
		ClientKey: "doc-client", ChannelID: 1, PeerType: channelstore.PeerChannel,
		Title: "Documented channel", MembershipStatus: channelstore.StatusMember,
	})
	if err != nil {
		p.t.Fatal(err)
	}
	category, err := store.CreateCategory(ctx, "Documented category", "Created by the documentation probe")
	if err != nil {
		p.t.Fatal(err)
	}
	if err := store.ReplacePermissions(ctx, admin.ID, channelstore.PermissionSet{
		Global:     channelstore.GlobalPermissions(),
		Channels:   []channelstore.ResourcePermissions{{ResourceID: channel.ID}},
		Categories: []channelstore.ResourcePermissions{{ResourceID: category.ID}},
	}); err != nil {
		p.t.Fatal(err)
	}
	// A blind pass (no deliberate grants) needs the limited account to hold at
	// least one resolvable resource, or no route is reachable with it at all.
	if err := store.ReplacePermissions(ctx, limited.ID, channelstore.PermissionSet{
		Global:     []channelstore.GlobalPermission{},
		Channels:   []channelstore.ResourcePermissions{{ResourceID: channel.ID}},
		Categories: []channelstore.ResourcePermissions{{ResourceID: category.ID}},
	}); err != nil {
		p.t.Fatal(err)
	}

	cache, err := avatarcache.Open(filepath.Join(p.t.TempDir(), "avatars"), avatarcache.Options{})
	if err != nil {
		p.t.Fatal(err)
	}
	tokens, err := auth.NewTokens(auth.TokenOptions{
		Secret: []byte(strings.Repeat("d", 48)),
		Now:    func() time.Time { return time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		p.t.Fatal(err)
	}
	p.tokens = tokens
	return gateway.New(gateway.Config{
		Workers: fakeRegistry{},
		Servers: fakeServers(),
		// The real store, as the daemon wires it: the examples that record a
		// channel (join, leave) and read one back hit the same SQLite the probe
		// seeded, so a documented response is one the application produces.
		Channels:   store,
		Categories: store,
		Users:      store,
		Tokens:     tokens,
		Avatars:    cache,
		Operator:   probeToken,
		Docs:       docsSite{},
		Now:        func() time.Time { return time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC) },
		// The WordPress routes are rehearsed the way the daemon wires them:
		// the real store, a working seal and the real client reading a
		// scripted site. Only the socket is scripted; the client's URL and
		// address rules still apply.
		WordPress:       store,
		Secrets:         probeEncrypter,
		WordPressClient: wordpress.NewClient(wordpress.WithTransport(scriptTransport{})),
	})
}

// docsSite is the documentation site the probe mounts at /docs: a tiny handler
// that answers every page request, so the route is reachable without the
// generator's rendered tree.
type docsSite struct{}

// probeEncrypter seals the probe's WordPress credentials with a fixed key that
// stands for the deployment's; it is not a credential for anything.
var probeEncrypter = func() *secrets.Encrypter {
	enc, err := secrets.NewEncrypter([]byte("telesync-docs-scripted-key-32by!"))
	if err != nil {
		panic(err)
	}
	return enc
}()

// scriptTransport hands every WordPress request to a scripted site, so the
// probe never opens a socket or reaches the network.
type scriptTransport struct{}

func (scriptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// The script answers any credential: the probe's examples are about the
	// gateway, not about WordPress authentication (that is covered in
	// internal/wordpress and internal/gateway).
	req.Header.Del("Authorization")
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	site := ScriptedSite(
		`{"title":"Example Publishing Site","site_icon":24}`,
		map[string]string{"24": `{"id":24,"source_url":"https://blog.example.com/icon.png"}`},
	)
	rec := httptest.NewRecorder()
	site.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	resp.ContentLength = int64(rec.Body.Len())
	resp.Body = io.NopCloser(strings.NewReader(rec.Body.String()))
	return resp, nil
}

func (docsSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><title>docs</title>"))
}

func (p *probe) Do(ctx context.Context, method, path, token, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req = req.WithContext(ctx)
	req.Header.Set("X-Request-Id", "probe")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	p.handler.ServeHTTP(rec, req)
	return rec
}

// anonymous is a caller with no credential.
type anonymous struct{}

func (anonymous) Authorization() string         { return "" }
func (anonymous) Snapshot(context.Context) bool { return true }
func (anonymous) Request() string               { return "" }
func (anonymous) ID() string                    { return "anonymous" }
func (p *probe) Anonymous() apidocs.Credential  { return anonymous{} }

// operator is the fixed token.
type operator struct{ token string }

func (o operator) Authorization() string              { return o.token }
func (o operator) Snapshot(context.Context) bool      { return true }
func (o operator) Request() string                    { return "" }
func (o operator) ID() string                         { return "operator" }
func (p *probe) Operator() (apidocs.Credential, bool) { return operator{probeToken}, true }

// account is one user's session, frozen when it is captured.
type account struct {
	p       *probe
	user    channelstore.User
	set     channelstore.PermissionSet
	token   string
	request string
}

func (a *account) Authorization() string { return a.token }
func (a *account) ID() string            { return fmt.Sprintf("user %d", a.user.ID) }
func (a *account) Request() string       { return a.request }

// Snapshot reports whether the account is still the caller it was captured
// as: the same active row, with the same stored permission set.
func (a *account) Snapshot(ctx context.Context) bool {
	current, found, err := a.p.store.GetUser(ctx, a.user.ID)
	if err != nil || !found || !current.IsActive {
		return false
	}
	if current.Username != a.user.Username || current.Fullname != a.user.Fullname {
		return false
	}
	now, err := a.p.store.PermissionsOf(ctx, a.user.ID)
	if err != nil {
		return false
	}
	// The set is compared by its JSON form: it is built from the same rows
	// every time, so an equal set renders identically.
	before, err := json.Marshal(a.set)
	if err != nil {
		return false
	}
	after, err := json.Marshal(now)
	if err != nil {
		return false
	}
	return string(before) == string(after)
}

// capture reads the account's current state and mints a token for it.
func (p *probe) capture(userID int64, request string) *account {
	p.t.Helper()
	user, found, err := p.store.GetUser(context.Background(), userID)
	if err != nil || !found {
		p.t.Fatalf("capture user %d: %v (found=%v)", userID, err, found)
	}
	set, err := p.store.PermissionsOf(context.Background(), user.ID)
	if err != nil {
		p.t.Fatal(err)
	}
	token, _, err := p.tokens.Issue(user.ID)
	if err != nil {
		p.t.Fatal(err)
	}
	return &account{p: p, user: user, set: set, token: token, request: request}
}

func (p *probe) userByUsername(name string) channelstore.User {
	p.t.Helper()
	u, found, err := p.store.UserByUsername(context.Background(), name)
	if err != nil || !found {
		p.t.Fatalf("user %q: %v", name, err)
	}
	return u
}

func (p *probe) Administrator() apidocs.Credential {
	return p.capture(p.userByUsername("admin").ID, "")
}

func (p *probe) Limited(put func(admin apidocs.Credential) string) apidocs.Credential {
	p.t.Helper()
	limited := p.userByUsername("doc-limited")
	path := fmt.Sprintf("/api/v1/local/users/%d/permissions", limited.ID)
	rec := p.Do(context.Background(), http.MethodPut, path, probeToken, put(p.Administrator()))
	if rec.Code != http.StatusOK {
		p.t.Fatalf("set the limited account's permissions: %d %s", rec.Code, rec.Body.String())
	}
	return p.capture(limited.ID, fmt.Sprintf("PUT %s", path))
}

// fakeWorker is the Telegram side: a fixed healthy client named doc-client.
type fakeWorker struct{}

func (fakeWorker) Health(context.Context) (rpc.Health, error) {
	return rpc.Health{Key: "doc-client", Type: "official", State: rpc.StateConnected,
		Authorized: true, Transport: "ready", RPCReady: true, PID: 1, ThisDC: 2}, nil
}

func (fakeWorker) Profile(context.Context) (rpc.Profile, error) {
	return rpc.Profile{Key: "doc-client", Type: "official", Authorized: true,
		AuthorizationState: rpc.AuthAuthorized, User: &rpc.Account{ID: 1}, ThisDC: 2}, nil
}

func (fakeWorker) Channels(context.Context, int, int) (rpc.ChannelPage, error) {
	return rpc.ChannelPage{
		Channels: []rpc.Channel{{ID: 1, Title: "Documented channel", Kind: "channel",
			LastMessageID: 2, Permissions: rpc.Permissions{Read: true}}},
		Page: rpc.PageInfo{Page: 1, PerPage: 20, Total: 1},
	}, nil
}

func (fakeWorker) Messages(context.Context, int64, int, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{Messages: []rpc.Message{fakeMessage()},
		Page: rpc.PageInfo{Page: 1, PerPage: 20}}, nil
}

func (fakeWorker) Message(context.Context, int64, int) (rpc.MessagePage, error) {
	return rpc.MessagePage{Messages: []rpc.Message{fakeMessage()}}, nil
}

func fakeMessage() rpc.Message {
	return rpc.Message{
		ID: 1, Date: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC), Text: "Documented post",
		Sender: &rpc.Sender{ID: 1, Name: "Documented", Username: "documented"},
		Media: &rpc.Media{ID: "1:1", Type: "photo", Label: "photo",
			MimeType: "image/jpeg", Size: 5},
	}
}

func (fakeWorker) Media(context.Context, int64, int, int) (rpc.MediaFile, error) {
	return rpc.MediaFile{MimeType: "text/plain", Data: []byte("bytes")}, nil
}

func (fakeWorker) Find(context.Context, rpc.FindRequest) (rpc.FindResult, error) {
	return rpc.FindResult{Channels: []rpc.ChannelInfo{{
		ID: "1", Type: "channel", Title: "Documented channel", IsMember: true,
		MembershipStatus: "member", AvatarAvailable: true, AvatarVersion: 9,
		Permissions: rpc.Permissions{Read: true},
	}}}, nil
}

func (fakeWorker) Channel(context.Context, rpc.ChannelRef) (rpc.ChannelResult, error) {
	return rpc.ChannelResult{Channel: rpc.ChannelInfo{
		ID: "1", Type: "channel", Title: "Documented channel", IsMember: true,
		MembershipStatus: "member", AvatarAvailable: true, AvatarVersion: 9,
		Permissions: rpc.Permissions{Read: true},
	}}, nil
}

func (fakeWorker) Join(context.Context, rpc.ChannelRef) (rpc.JoinResult, error) {
	return rpc.JoinResult{Channel: rpc.ChannelInfo{
		ID: "1", Type: "channel", Title: "Documented channel", IsMember: true,
		MembershipStatus: "member", AvatarAvailable: true, AvatarVersion: 9,
		Permissions: rpc.Permissions{Read: true},
	}, Outcome: rpc.OutcomeJoined}, nil
}

func (fakeWorker) CustomEmoji(context.Context, int64) (rpc.MediaFile, error) {
	return rpc.MediaFile{MimeType: "application/x-tgsticker", Data: []byte("emoji-bytes")}, nil
}

func (fakeWorker) SendMessage(_ context.Context, req rpc.SendRequest) (rpc.SendResult, error) {
	return rpc.SendResult{
		Message:   &rpc.Message{ID: 12345, Text: req.Text, Date: time.Now().UTC()},
		ChannelID: strconv.FormatInt(req.ChannelID, 10),
	}, nil
}

func (fakeWorker) Avatar(context.Context, rpc.ChannelRef) (rpc.AvatarFile, error) {
	return rpc.AvatarFile{MimeType: "image/jpeg", PhotoID: 9, Data: []byte("jpeg-bytes")}, nil
}

type fakeRegistry struct{}

func (fakeRegistry) Worker(key string) (gateway.Worker, bool) {
	// "official" is the key the documentation's examples use.
	switch key {
	case "doc-client", "official":
		return fakeWorker{}, true
	}
	return nil, false
}

// fakeServers is what GET /api/v1/telegram lists in the probe: the client the
// examples use, and one this deployment does not run.
func fakeServers() []gateway.ServerInfo {
	return []gateway.ServerInfo{
		{Key: "official", Type: "official", Enabled: true, Registered: true},
		{Key: "doc-client", Type: "official", Enabled: true, Registered: true},
	}
}
