package panel

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/gateway"
)

// The panel the binary carries is the one `make build` embedded: a page at the
// root and at least one script for the browser to run.
func TestEmbeddedPanelHasAPage(t *testing.T) {
	page, err := fs.ReadFile(Files(), "index.html")
	if err != nil {
		t.Fatalf("the binary carries no panel page: %v", err)
	}
	html := string(page)
	if !strings.Contains(html, `<html lang="fa" dir="rtl">`) {
		t.Fatalf("the page is not the Persian, right-to-left one: %s", html)
	}
	// The router hydrates this page: the context and the module map are what it
	// reads before it starts, and the entry is what it runs.
	if !strings.Contains(html, "window.__reactRouterContext") ||
		!strings.Contains(html, "window.__reactRouterRouteModules") ||
		!strings.Contains(html, `type="module"`) {
		t.Fatalf("the page has no context, no route modules or nothing to run: %s", html)
	}
	if !strings.Contains(html, `href="/admin/`) || !strings.Contains(html, `"/admin/assets/`) {
		t.Fatalf("the page's links are not absolute under /admin: %s", html)
	}
}

// The daemon's wiring, end to end: the gateway mounted at /admin answers its
// page for a client-side route and serves the built assets beside it.
func TestPanelServedUnderTheBasePath(t *testing.T) {
	h := gateway.New(gateway.Config{
		BasePath: "/admin",
		Panel:    http.StripPrefix("/admin", http.FileServerFS(Files())),
	})

	for _, path := range []string{"/admin", "/admin/login", "/admin/channels"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "window.__reactRouterContext") {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
	}

	// One built asset, named by the page itself.
	page, _ := fs.ReadFile(Files(), "index.html")
	src := between(string(page), `href="/admin/`, `"`)
	if src == "" {
		t.Fatal("the page names no file")
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/"+src, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("GET the page's script = %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func between(s, after, before string) string {
	_, rest, ok := strings.Cut(s, after)
	if !ok {
		return ""
	}
	value, _, _ := strings.Cut(rest, before)
	return value
}
