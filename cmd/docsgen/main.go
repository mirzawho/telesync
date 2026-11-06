// Command docsgen renders docs/api/generated from the gateway's source and a
// replay of its examples, and composes the pages under docs/api/manual that
// mix hand-written prose with generated endpoint facts.
//
//	go run ./cmd/docsgen            write the pages
//	go run ./cmd/docsgen --check    fail if the committed pages are outdated
//
// It needs no network, no credentials and no LLM: the observations come from
// an in-process gateway over a temporary SQLite database, and the facts are
// read from the Go source with go/ast.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/apidocs"
	"github.com/mirza/telesync/internal/appconfig"
	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/automation/queue"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/automation/workflows"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/docsgen"
	"github.com/mirza/telesync/internal/gateway"
	"github.com/mirza/telesync/internal/secrets"
	"github.com/mirza/telesync/internal/tgapi"
	"github.com/mirza/telesync/internal/wordpress"
)

func main() {
	check := flag.Bool("check", false, "compare the committed documentation with a fresh generation and exit non-zero if it differs; nothing is written")
	root := flag.String("root", ".", "the repository root (holding docs/api and internal/gateway)")
	flag.Parse()

	if err := run(context.Background(), *root, *check, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "docsgen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root string, check bool, stdout, stderr io.Writer) error {
	prober, closeFn, err := newProber(ctx)
	if err != nil {
		return err
	}
	defer closeFn()

	static, err := gateway.NewDocsFS(gateway.OsDocs(docsgen.DocsDir(root)))
	if err != nil {
		return err
	}
	plan, err := docsgen.Build(ctx, docsgen.Options{RepoRoot: root, Probe: prober, Static: static})
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, plan.Summary())
	if check {
		if plan.Outdated() {
			return fmt.Errorf("the committed documentation is outdated; run `go run ./cmd/docsgen` and commit the result")
		}
		fmt.Fprintln(stdout, "documentation is up to date")
		return nil
	}
	if err := plan.Write(root); err != nil {
		return err
	}
	// The binary carries the documentation, so every generation refreshes the
	// copy the compiler embeds: `make build` needs no docs_dir beside it.
	embedDir := filepath.Join(root, docsgen.EmbedDir)
	if err := docsgen.Embed(root, embedDir); err != nil {
		return fmt.Errorf("embed the documentation: %w", err)
	}
	fmt.Fprintf(stdout, "embedded %s for the binary\n", docsgen.EmbedDir)
	return nil
}

// newProber is the gateway the generator observes: a real SQLite store in a
// temporary directory and a fake Telegram worker.
func newProber(ctx context.Context) (apidocs.Prober, func(), error) {
	dir, err := os.MkdirTemp("", "docsgen-")
	if err != nil {
		return nil, nil, err
	}
	store, err := channelstore.Open(dir+"/docs.db", channelstore.Options{Now: probeClock})
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	p := &prober{store: store}
	if err := p.seed(ctx); err != nil {
		store.Close()
		os.RemoveAll(dir)
		return nil, nil, err
	}
	return p, func() { store.Close(); os.RemoveAll(dir) }, nil
}

// probeClock is a fixed clock: two runs must produce the same bytes, so the
// gateway's timestamps cannot come from the wall clock.
var probeClock = func() time.Time { return time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC) }

const (
	// operatorToken is the token the probe configures. It is a placeholder,
	// not a credential.
	operatorToken = "docsgen-operator-token"
	// accountPassword is the password of the probe accounts.
	accountPassword = "example-doc-password"
)

type prober struct {
	store   *channelstore.Store
	handler http.Handler
	tokens  *auth.Tokens
}

func (p *prober) seed(ctx context.Context) error {
	hash, err := auth.HashPassword(accountPassword)
	if err != nil {
		return err
	}
	admin, err := p.store.CreateUser(ctx, "Documented Administrator", "admin", hash, true)
	if err != nil {
		return err
	}
	limited, err := p.store.CreateUser(ctx, "Documented Limited User", "doc-limited", hash, true)
	if err != nil {
		return err
	}
	if _, err := p.store.CreateUser(ctx, "Documented Other User", "doc-other", hash, true); err != nil {
		return err
	}
	// The record as the join route writes one: a snapshot of what the client
	// reported, so the local channel detail documents a complete channel.
	channel, err := p.store.Upsert(ctx, channelstore.Channel{
		ClientKey: "doc-client", ChannelID: 1, PeerType: channelstore.PeerChannel,
		Title: "Documented channel", MembershipStatus: channelstore.StatusMember,
		AvatarAvailable: true, NoticesKnown: true,
		Permissions: tgapi.Permissions{Read: true}, PermissionsKnown: true,
	})
	if err != nil {
		return err
	}
	category, err := p.store.CreateCategory(ctx, "Documented category", "Created by the documentation generator")
	if err != nil {
		return err
	}
	if err := p.store.ReplacePermissions(ctx, admin.ID, channelstore.PermissionSet{
		Global:     channelstore.GlobalPermissions(),
		Channels:   []channelstore.ResourcePermissions{{ResourceID: channel.ID}},
		Categories: []channelstore.ResourcePermissions{{ResourceID: category.ID}},
	}); err != nil {
		return err
	}
	if err := p.store.ReplacePermissions(ctx, limited.ID, channelstore.PermissionSet{
		Global:     []channelstore.GlobalPermission{},
		Channels:   []channelstore.ResourcePermissions{{ResourceID: channel.ID}},
		Categories: []channelstore.ResourcePermissions{{ResourceID: category.ID}},
	}); err != nil {
		return err
	}
	tokens, err := auth.NewTokens(auth.TokenOptions{
		Secret: []byte(strings.Repeat("d", 48)),
		Now:    probeClock,
	})
	if err != nil {
		return err
	}
	p.tokens = tokens
	p.handler = gateway.New(gateway.Config{
		Now:     probeClock,
		Workers: workerRegistry{},
		Servers: documentedServers(),
		// The local channel record the join route writes: one documented row,
		// so the local channel pages observe a complete channel. The generated
		// exchanges byte-compare between runs, so this stays in memory rather
		// than reading the database that records a join's own timestamps.
		Channels:   managedStore{},
		Categories: p.store,
		Users:      p.store,
		Tokens:     tokens,
		Operator:   operatorToken,
		// The WordPress routes are observed the way the daemon wires them: the
		// real store, a fixed seal and the real client reading a scripted site.
		// Only the socket is scripted; the client's URL and address rules stay
		// in force, and the fixed clock keeps two runs identical.
		WordPress:       p.store,
		Secrets:         probeSeal,
		WordPressClient: wordpress.NewClient(wordpress.WithTransport(scriptedSite(probeClock))),
		// The automation routes are observed the way the daemon wires them:
		// the real webhook and workflow services over the real store and the
		// real queue, with the clock fixed so two runs of the generator produce
		// byte-identical exchanges. Only the receiver is scripted.
		AutomationWebhooks:  docWebhookService(p.store, probeSeal, probeClock),
		AutomationWorkflows: &workflows.Service{Store: p.store},
		AutomationStatus:    &workflows.Status{Store: p.store},
		PublicBaseURL:       docPublicBaseURL,
		AutomationEnabled:   true,
		AutomationWorkers:   2,
	})
	return nil
}

// docPublicBaseURL stands for api.public_base_url: the origin a webhook
// receiver resolves a media link against. Nothing connects to it.
const docPublicBaseURL = "https://telesync.example"

// docWebhookService builds the real webhook service over the probe's store.
func docWebhookService(store *channelstore.Store, box webhooks.Secrets, now func() time.Time) *webhooks.Service {
	enqueuer := queue.NewEnqueuer(store, 5)
	svc, err := webhooks.New(webhooks.Config{
		PublicBaseURL: docPublicBaseURL,
		Webhooks: appconfig.WebhookConfig{
			Timeout:                 15 * time.Second,
			MaxAttempts:             5,
			MaxResponseBytes:        64 << 10,
			MaxConcurrentDeliveries: 4,
		},
		Store:     store,
		Secrets:   box,
		Events:    store.AutomationEvent,
		Jobs:      enqueuer,
		Reopen:    enqueuer.Reopen,
		Now:       now,
		Transport: scriptedReceiver{},
	})
	if err != nil {
		panic(err)
	}
	return svc
}

// scriptedReceiver stands in for the endpoint a webhook posts to, as
// scriptedSite stands in for WordPress. The request, its signature and the
// attempt record are all real; only the socket is absent.
type scriptedReceiver struct{}

func (scriptedReceiver) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"received":true}`)),
	}, nil
}

// probeSeal seals the probe's WordPress credentials. The key is a placeholder
// for the deployment's, never a real one.
var probeSeal = func() *secrets.Encrypter {
	enc, err := secrets.NewEncrypter([]byte("docsgen-scripted-key-32-bytes!!!"))
	if err != nil {
		panic(err)
	}
	return enc
}()

// scriptedSite answers every WordPress request from a fixed site, so the
// generator never opens a socket. The credential is not checked: these
// exchanges document the gateway, not WordPress authentication. The argument
// keeps the constructor's call site readable.
type scriptedSite func() time.Time

func (s scriptedSite) RoundTrip(req *http.Request) (*http.Response, error) {
	status := http.StatusOK
	body := `{"title":"Example publishing site","site_icon":24}`
	switch {
	case strings.HasSuffix(req.URL.Path, "/wp-json/wp/v2/media/24"):
		body = `{"id":24,"source_url":"https://blog.example.com/icon.png"}`
	case strings.HasSuffix(req.URL.Path, "/wp-json/wp/v2/settings"):
	default:
		status = http.StatusNotFound
		body = `{"code":"rest_no_route"}`
	}
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{"application/json; charset=UTF-8"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

func (scriptedSite) CloseIdleConnections() {}

// build-time assertion that the prober's client field takes this transport.
var _ http.RoundTripper = scriptedSite(nil)

func (p *prober) Do(_ context.Context, method, path, token, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	// A fixed request id, so two runs produce the same pages. The probe's
	// requests never share the process with real traffic.
	req.Header.Set("X-Request-Id", "docsgen")
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

type anonymousCaller struct{}

func (anonymousCaller) Authorization() string         { return "" }
func (anonymousCaller) Snapshot(context.Context) bool { return true }
func (anonymousCaller) Request() string               { return "" }
func (anonymousCaller) ID() string                    { return "anonymous" }

type operatorCaller struct{}

func (operatorCaller) Authorization() string         { return operatorToken }
func (operatorCaller) Snapshot(context.Context) bool { return true }
func (operatorCaller) Request() string               { return "" }
func (operatorCaller) ID() string                    { return "operator" }

type accountCaller struct {
	p       *prober
	userID  int64
	token   string
	set     channelstore.PermissionSet
	request string
}

func (a *accountCaller) Authorization() string { return a.token }
func (a *accountCaller) ID() string            { return fmt.Sprintf("user %d", a.userID) }
func (a *accountCaller) Request() string       { return a.request }

// Snapshot is true while the account is still active with the same set.
func (a *accountCaller) Snapshot(ctx context.Context) bool {
	user, found, err := a.p.store.GetUser(ctx, a.userID)
	if err != nil || !found || !user.IsActive {
		return false
	}
	now, err := a.p.store.PermissionsOf(ctx, a.userID)
	if err != nil {
		return false
	}
	return fmt.Sprint(now.Global) == fmt.Sprint(a.set.Global) &&
		fmt.Sprint(now.Channels) == fmt.Sprint(a.set.Channels) &&
		fmt.Sprint(now.Categories) == fmt.Sprint(a.set.Categories)
}

func (p *prober) Anonymous() apidocs.Credential { return anonymousCaller{} }

func (p *prober) Operator() (apidocs.Credential, bool) { return operatorCaller{}, true }

func (p *prober) Administrator() apidocs.Credential {
	user, _, err := p.store.UserByUsername(context.Background(), "admin")
	if err != nil {
		panic(err)
	}
	return p.capture(user.ID, "")
}

func (p *prober) Limited(put func(admin apidocs.Credential) string) apidocs.Credential {
	user, _, err := p.store.UserByUsername(context.Background(), "doc-limited")
	if err != nil {
		panic(err)
	}
	path := fmt.Sprintf("/api/v1/local/users/%d/permissions", user.ID)
	rec := p.Do(context.Background(), http.MethodPut, path, operatorToken, put(p.Administrator()))
	if rec.Code != http.StatusOK {
		panic(fmt.Sprintf("set the limited account's permissions: %d %s", rec.Code, rec.Body))
	}
	return p.capture(user.ID, "PUT "+path)
}

func (p *prober) capture(userID int64, request string) apidocs.Credential {
	set, err := p.store.PermissionsOf(context.Background(), userID)
	if err != nil {
		panic(err)
	}
	token, _, err := p.tokens.Issue(userID)
	if err != nil {
		panic(err)
	}
	return &accountCaller{p: p, userID: userID, token: token, set: set, request: request}
}
