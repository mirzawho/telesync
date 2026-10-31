package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/gateway"
)

// The panel tree as `make panel` embeds it: one page whose links are absolute,
// under the mount prefix it was built for, and its assets.
var panelTree = fstest.MapFS{
	"index.html": {Data: []byte(`<!doctype html><html lang="fa" dir="rtl"><head>` +
		`<link rel="stylesheet" href="/admin/assets/app.css"></head>` +
		`<body><div id="root"></div><script type="module" src="/admin/assets/app.js"></script></body></html>`)},
	"assets/app.js":  {Data: []byte(`console.log("panel")`)},
	"assets/app.css": {Data: []byte(`body{color:red}`)},
	"favicon.svg":    {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
}

// newPanelEnv is the gateway mounted at /admin with the panel beside its API,
// the way the daemon serves it.
func newPanelEnv(t *testing.T) *accountEnv {
	t.Helper()
	return newAccountEnv(t, registry{"official": readyWorker()}, func(c *gateway.Config) {
		c.BasePath = "/admin"
		c.Panel = http.StripPrefix("/admin", http.FileServerFS(panelTree))
		c.Docs = pageSite{}
	})
}

// pageSite answers every documentation path with a page, so the docs routes
// are reachable without the generator's rendered tree.
type pageSite struct{}

func (pageSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("DOCS-PAGE"))
}

// anonymous builds a request with no credential: send reserves "" for the
// suite's default caller, so this one is made directly.
func (env *accountEnv) anonymous(t *testing.T, method, path string) *http.Response {
	t.Helper()
	return env.doRaw(t, httptest.NewRequest(method, path, nil))
}

// The panel is embedded, not served from disk: every route of the app renders
// its one page, and the page's own links are what the mount expects.
func TestPanelPageAndAssetsAreServedUnderTheBasePath(t *testing.T) {
	env := newPanelEnv(t)

	for _, path := range []string{"/admin", "/admin/", "/admin/login", "/admin/categories"} {
		resp := env.anonymous(t, http.MethodGet, path)
		body := bodyOf(resp)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, `<div id="root">`) {
			t.Fatalf("GET %s = %d, want the panel page: %s", path, resp.StatusCode, body)
		}
		if !strings.Contains(body, `href="/admin/assets/app.css"`) ||
			!strings.Contains(body, `src="/admin/assets/app.js"`) {
			t.Fatalf("GET %s: the page's links are not under /admin: %s", path, body)
		}
	}

	// The files are requested the way a browser requests them: no credential,
	// and fetch metadata that says a script is wanted, not a page.
	files := map[string]string{
		"/admin/assets/app.js":  `console.log`,
		"/admin/assets/app.css": `color:red`,
		"/admin/favicon.svg":    `<svg`,
	}
	for path, want := range files {
		hreq := httptest.NewRequest(http.MethodGet, path, nil)
		hreq.Header.Set("Sec-Fetch-Dest", "script")
		resp := env.doRaw(t, hreq)
		if resp.StatusCode != http.StatusOK || !strings.Contains(bodyOf(resp), want) {
			t.Fatalf("GET %s = %d, want %q", path, resp.StatusCode, want)
		}
		head := httptest.NewRequest(http.MethodHead, path, nil)
		head.Header.Set("Sec-Fetch-Dest", "script")
		if resp := env.doRaw(t, head); resp.StatusCode != http.StatusOK {
			t.Fatalf("HEAD %s = %d, want 200", path, resp.StatusCode)
		}
	}

	// Nothing else is the panel: a missing asset is a 404, not the page.
	missing := httptest.NewRequest(http.MethodGet, "/admin/assets/missing.js", nil)
	missing.Header.Set("Sec-Fetch-Dest", "script")
	if resp := env.doRaw(t, missing); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing asset = %d, want 404", resp.StatusCode)
	}
}

// The pages are not the API: a browser can open the panel, its files, the
// documentation and the health check with no credential, and only /admin/api
// asks for one.
func TestPagesNeedNoCredential(t *testing.T) {
	env := newPanelEnv(t)
	for _, path := range []string{"/admin", "/admin/login", "/admin/dashboard", "/admin/healthz", "/admin/docs"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		if resp := env.doRaw(t, req); resp.StatusCode != http.StatusOK {
			t.Errorf("anonymous GET %s = %d", path, resp.StatusCode)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/v1/auth/me", nil)
	if resp := env.doRaw(t, req); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous me = %d, want 401", resp.StatusCode)
	}
}

// A page navigation (the browser asks for a document) that names no file is
// the app's own route; a fetch for data is not, and gets no page.
func TestPanelPageIsOnlyForNavigations(t *testing.T) {
	env := newPanelEnv(t)
	hreq := httptest.NewRequest(http.MethodGet, "/admin/categories", nil)
	hreq.Header.Set("Accept", "text/html,application/xhtml+xml")
	if resp := env.doRaw(t, hreq); resp.StatusCode != http.StatusOK {
		t.Fatalf("a navigation = %d, want the page", resp.StatusCode)
	}

	hreq = httptest.NewRequest(http.MethodGet, "/admin/data.json", nil)
	hreq.Header.Set("Accept", "application/json")
	if resp := env.doRaw(t, hreq); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a data request for an unknown path = %d, want 404", resp.StatusCode)
	}
}

// The API is mounted beside the panel: the panel's server actions call these
// paths on the same origin, with the bearer token the login issued.
func TestAPIAnswersUnderTheBasePath(t *testing.T) {
	env := newPanelEnv(t)
	who := env.account(t, "alice", channelstore.PermissionUsersView)

	hreq := httptest.NewRequest(http.MethodPost, "/admin/api/v1/auth/login",
		strings.NewReader(`{"username":"alice","password":"password-alice"}`))
	hreq.Header.Set("Content-Type", "application/json")
	resp := env.doRaw(t, hreq)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login under /admin = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	token := decode[loginResponse](t, resp).AccessToken
	if token == "" {
		t.Fatal("login under /admin issued no token")
	}

	if resp := env.send(t, token, http.MethodGet, "/admin/api/v1/auth/me", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("me under /admin = %d: %s", resp.StatusCode, bodyOf(resp))
	}
	if resp := env.send(t, who.token, http.MethodGet, "/admin/api/v1/local/categories", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("categories under /admin = %d: %s", resp.StatusCode, bodyOf(resp))
	}

	// With a mount, the API lives only under it: the root paths are not the
	// API's. A browser asking for the root gets a page; a script gets nothing.
	outside := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	outside.Header.Set("Accept", "application/json")
	if resp := env.doRaw(t, outside); resp.StatusCode == http.StatusOK {
		t.Fatal("the API still answers outside /admin")
	}
}

// A refused request names the login page at its mounted address, so the hint
// a caller reads is a URL that exists.
func TestUnauthenticatedRequestNamesTheMountedLogin(t *testing.T) {
	env := newPanelEnv(t)

	hreq := httptest.NewRequest(http.MethodGet, "/admin/api/v1/auth/me", nil)
	hreq.Header.Set("Accept", "application/json")
	resp := env.doRaw(t, hreq)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me without a token = %d, want 401", resp.StatusCode)
	}
	if body := bodyOf(resp); !strings.Contains(body, "/admin/api/v1/auth/login") {
		t.Fatalf("the login hint does not name the mounted login: %s", body)
	}
}
