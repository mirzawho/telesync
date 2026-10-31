package docsite

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mirza/telesync/internal/gateway"
)

func gatewaySite(tree fs.FS) (http.Handler, error) { return gateway.NewDocsFS(tree) }

func serve(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The site the daemon builds from the embedded tree must answer without a
// credential and publish what the generator wrote: this is the path
// `make build` + `./bin/telesync daemon` + GET /docs takes, with no docs_dir.
func TestEmbeddedTreeRendersThroughTheGateway(t *testing.T) {
	site := fstest.MapFS{
		"README.md":              {Data: []byte("<!-- title: API -->\n# API\n\nSee [endpoints](generated/endpoints.md).\n")},
		"generated/endpoints.md": {Data: []byte("<!-- generated: endpoints -->\n# Endpoints\n")},
	}
	h, err := gatewaySite(site)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/docs", "/docs/generated/endpoints.md"} {
		rec := serve(h, p, "")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<main>") {
			t.Fatalf("%s: status %d body %q", p, rec.Code, rec.Body.String())
		}
	}
	if rec := serve(h, "/docs/not-published.md", ""); rec.Code != 404 {
		t.Fatalf("a stray page answered %d", rec.Code)
	}
}

// The embedded copy is itself a tree the renderer accepts, not just whatever a
// fixture says: this catches a generator that stops signing its pages.
func TestEmbeddedCopyIsServed(t *testing.T) {
	h, err := gatewaySite(FS())
	if err != nil {
		t.Fatalf("the embedded tree is not a documentation site: %v (run make docs)", err)
	}
	rec := serve(h, "/docs", "")
	if rec.Code != 200 {
		t.Fatalf("/docs on the embedded tree: %d", rec.Code)
	}
}
