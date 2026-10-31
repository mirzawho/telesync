package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/gateway"
)

// The documentation site is served at /docs and /docs/... from one directory of
// Markdown. These tests exercise the handler directly (its routing and
// rendering) and through the gateway (its place among the credentials).

// docsSite builds a documentation root with the given files and a handler on
// it, with no gateway in front.
func docsSite(t *testing.T, files map[string]string) (*docsHandlerForTest, string) {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := gateway.NewDocsFS(gateway.OsDocs(root))
	if err != nil {
		t.Fatal(err)
	}
	return &docsHandlerForTest{t: t, h: h}, root
}

type docsHandlerForTest struct {
	t *testing.T
	h http.Handler
}

func (d *docsHandlerForTest) get(method, target string) *httptest.ResponseRecorder {
	d.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	d.h.ServeHTTP(rec, req)
	return rec
}

var docsFixture = map[string]string{
	"README.md": "<!-- title: telesync API -->\n# telesync API\n\nSee [auth](auth.md) and [telegram](telegram/README.md).\n\n" +
		"[back to root](../README.md)\n",
	"auth.md": "<!-- manual: authentication -->\n# Authentication\n\n| Field | Type |\n| --- | --- |\n| `token` | string |\n\n" +
		"```bash\ncurl --fail \"${BASE_URL}/api/v1/auth/me\"\n```\n\n" +
		"Use `the token` here. Raw <script>alert('x')</script> must not run.\n\n" +
		"[bad](javascript:alert(1))\n",
	"telegram/README.md": "<!-- manual: telegram -->\n# Telegram routes\n\nNested index.\n",
	"telegram/channels.md": "<!-- manual: channels -->\n# Channels\n\n" +
		"## List\n\n- one\n- two\n\n[schema](../schemas.md)\n",
	"schemas.md":           "<!-- manual: schemas -->\n# Schemas\n",
	"notes/secret.txt":     "not markdown",
	"notes/README.md":      "<!-- manual: notes -->\n# Notes\n\nNested notes.\n",
	"stray.md":             "# Stray\n\nA file that names no writer.\n",
	"schemas/example.json": `{"not":"markdown"}`,
}

func TestDocsLandingPageRenders(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	resp := site.get(http.MethodGet, "/docs")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d", resp.Code)
	}
	if ct := resp.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", ct)
	}
	body := resp.Body.String()
	for _, want := range []string{"<!doctype html>", `<h1 id="telesync-api">telesync API</h1>`, "telesync docs", `href="/docs/auth.md"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the landing page lacks %q", want)
		}
	}
	// /docs/ and /docs/README.md name the same page.
	for _, target := range []string{"/docs/", "/docs/README.md"} {
		if other := site.get(http.MethodGet, target); other.Code != http.StatusOK {
			t.Errorf("%s: status = %d", target, other.Code)
		}
	}
}

func TestDocsMarkdownRendersTablesCodeAndLinks(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	body := site.get(http.MethodGet, "/docs/auth.md").Body.String()
	for _, want := range []string{
		`<h1 id="authentication">Authentication</h1>`,
		"<table>", "<th>Field</th>", "<code>token</code>",
		"<pre><code class=\"language-bash\">", "api/v1/auth/me",
		"<code>the token</code>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the rendered page lacks %q", want)
		}
	}
	// A relative link to a document becomes its route; an anchor survives.
	if !strings.Contains(body, `href="/docs/telegram/channels.md"`) {
		t.Errorf("a relative document link was not rewritten to its route: %s", body)
	}
}

func TestDocsNestedPagesAndRelativeLinks(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	for target, wants := range map[string][]string{
		"/docs/telegram/":            {"Telegram routes"},
		"/docs/telegram/README.md":   {"Telegram routes"},
		"/docs/telegram/channels.md": {"Channels", `href="/docs/schemas.md"`, "<h2 id=\"list\">List</h2>"},
	} {
		resp := site.get(http.MethodGet, target)
		if resp.Code != http.StatusOK {
			t.Errorf("%s: status = %d", target, resp.Code)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(resp.Body.String(), want) {
				t.Errorf("%s lacks %q", target, want)
			}
		}
	}
	// The sidebar links the same nested route the page has.
	if body := site.get(http.MethodGet, "/docs/auth.md").Body.String(); !strings.Contains(body, `href="/docs/telegram/channels.md"`) {
		t.Error("the sidebar does not link a nested page")
	}
}

func TestDocsMissingAndUnsupportedFilesAre404(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	for _, target := range []string{
		"/docs/missing.md",
		"/docs/notes/secret.txt",     // not Markdown
		"/docs/schemas/example.json", // not Markdown
		"/docs/notes/",               // the fixture makes this a page, not a 404
		"/docs/stray.md",             // names no writer: not published
	} {
		resp := site.get(http.MethodGet, target)
		if target == "/docs/notes/" {
			// The directory route serves its README.md when it has one.
			if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "Nested") {
				t.Errorf("%s: status = %d, want its README", target, resp.Code)
			}
			continue
		}
		if resp.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", target, resp.Code)
		}
		if ct := resp.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: content type = %q", target, ct)
		}
	}
}

func TestDocsTraversalIsBlocked(t *testing.T) {
	site, root := docsSite(t, docsFixture)
	outside := filepath.Join(filepath.Dir(root), "outside.md")
	if err := os.WriteFile(outside, []byte("# Secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"/docs/../outside.md",
		"/docs/..%2foutside.md",
		"/docs/%2e%2e/outside.md",
		"/docs/%2e%2e%2foutside.md",
		"/docs/telegram/../../outside.md",
		"/docs/telegram/%2e%2e/%2e%2e/outside.md",
		"/docs//etc/passwd",
		"/docs/%2Fetc%2Fpasswd",
	} {
		resp := site.get(http.MethodGet, target)
		if resp.Code == http.StatusOK && strings.Contains(resp.Body.String(), "Secret") {
			t.Errorf("%s served a file outside the root", target)
		}
		if resp.Code != http.StatusNotFound && resp.Code != http.StatusBadRequest &&
			resp.Code != http.StatusMovedPermanently {
			t.Errorf("%s: status = %d, want a refusal", target, resp.Code)
		}
	}
}

func TestDocsSymlinkOutsideTheRootIsRefused(t *testing.T) {
	site, root := docsSite(t, docsFixture)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# Secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.md")); err != nil {
		t.Skip("symlinks are not available here")
	}
	// The site lists what it finds at startup; a link added afterwards is not
	// listed, so this also covers the allow-list.
	if resp := site.get(http.MethodGet, "/docs/linked.md"); resp.Code != http.StatusNotFound {
		t.Fatalf("a symlink outside the root: status = %d", resp.Code)
	}
}

func TestDocsRawHTMLAndDangerousLinksAreInert(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	body := site.get(http.MethodGet, "/docs/auth.md").Body.String()
	if strings.Contains(body, "<script>alert('x')</script>") {
		t.Fatal("raw HTML from a document reached the page unescaped")
	}
	// Raw HTML must never reach the page as markup. Goldmark's default does
	// exactly that ("<!-- raw HTML omitted -->"); one that escapes it instead
	// is equally safe, so both are accepted.
	if strings.Contains(body, "<script") {
		t.Error("raw HTML reached the page as markup")
	}
	if !strings.Contains(body, "<!-- raw HTML omitted -->") && !strings.Contains(body, "&lt;script&gt;") {
		t.Error("raw HTML should be replaced by a marker or escaped, not rendered")
	}
	if strings.Contains(body, "<script") {
		t.Error("raw HTML reached the page as markup")
	}
	if strings.Contains(body, `href="javascript:`) {
		t.Fatal("a javascript: link reached the page as a link")
	}
	// The page loads and runs nothing of its own.
	resp := site.get(http.MethodGet, "/docs/auth.md")
	csp := resp.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("content security policy = %q", csp)
	}
	if resp.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff is required")
	}
}

func TestDocsMethodNotAllowed(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	resp := site.get(http.MethodPost, "/docs/auth.md")
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", resp.Code)
	}
	if allow := resp.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow = %q", allow)
	}
	if head := site.get(http.MethodHead, "/docs/auth.md"); head.Code != http.StatusOK {
		t.Fatalf("HEAD: status = %d", head.Code)
	}
}

// TestGatewayDocsArePublic proves /docs answers without a credential while the
// API routes keep their own policy.
func TestGatewayDocsArePublic(t *testing.T) {
	site, _ := docsSite(t, docsFixture)
	worker := readyWorker()
	env := newEnv(t, registry{"official": worker}, func(c *gateway.Config) {
		c.Docs = site.h
		c.Operator = "operator-token"
	})

	for _, target := range []string{"/docs", "/docs/", "/docs/auth.md"} {
		resp := env.get(t, target)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s without a credential: status = %d", target, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("%s: content type = %q", target, ct)
		}
	}
	// A wrong token does not matter on a public page.
	if wrong := env.get(t, "/docs/auth.md", "Authorization", "Bearer wrong"); wrong.StatusCode != http.StatusOK {
		t.Fatalf("a wrong token on a public page: status = %d", wrong.StatusCode)
	}
	// The API routes are untouched by the documentation branch.
	resp := env.get(t, "/api/v1/telegram/official/health")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an API route must still require the operator token: status = %d", resp.StatusCode)
	}
	resp = env.get(t, "/api/v1/telegram/official/health", "Authorization", "Bearer operator-token")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an API route with the token: status = %d", resp.StatusCode)
	}
}
