package apidocs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/apidocs"
)

// TestDocumentationCoversEveryRoute is the direction the probe cannot test: a
// route that is served but missing from the table, or present in the table but
// missing from the documentation, fails here.
//
// The generated pages mention every route in two ways: a table row with the
// method and path, and a page heading for each pattern. A route in docs/api
// that the gateway does not serve fails too, so a removed route cannot linger.
func TestDocumentationCoversEveryRoute(t *testing.T) {
	documented, err := apidocs.DocRoutes(filepath.Join(docsDir, "generated"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := apidocs.DocRoutes(filepath.Join(docsDir, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	for key := range legacy {
		// The legacy and generated tables write variables with their names
		// ({key}, {channel_id}); the comparison uses one placeholder.
		documented[key] = true
	}

	// DocRoutes reads a row's path as a concrete path with {any} for every
	// variable; the probe's own table writes "{path}", so an extra name is
	// accepted for a variable that sits where the table's variable sits.
	var missing []string
	for _, r := range apidocs.Routes {
		for _, m := range r.Methods {
			// The generated pages write each variable by name ({key},
			// {channel_id}); any non-empty segment matches a variable, so a
			// placeholder of the right shape is a probe path to Serves.
			documentedPath := apidocs.Concrete(r.Pattern)
			if !documented[m+" "+documentedPath] && !documentedByShape(documented, m, r.Pattern) {
				missing = append(missing, m+" "+apidocs.VariablePath(r.Pattern))
			}
		}
	}
	for _, key := range missing {
		t.Errorf("%s is served but no generated page documents it; run go run ./cmd/docsgen and add a manual section", key)
	}

	for key := range documented {
		method, path, ok := strings.Cut(key, " ")
		if !ok || !apidocs.Serves(method, path) {
			t.Errorf("%s is documented but the gateway does not serve it", key)
		}
	}
}

// documentedByShape reports whether a documented row is the route's pattern,
// whatever its variables are called.
func documentedByShape(documented map[string]bool, method, pattern string) bool {
	for key := range documented {
		m, path, ok := strings.Cut(key, " ")
		if ok && m == method && apidocs.Matches(pattern, path) && apidocs.Matches(path, apidocs.Concrete(pattern)) {
			return true
		}
	}
	return false
}

// TestGeneratorIsOffline proves the extraction path has no network, no shell
// and no model client in it: the generator reads the Go source and an
// in-process gateway, and nothing else.
//
// It is a source-level check because the packages it guards are what produce
// the documentation: an import added there would otherwise be noticed only by
// someone reading the report's claims.
func TestGeneratorIsOffline(t *testing.T) {
	files, err := filepath.Glob("../../internal/docsgen/*.go")
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := filepath.Glob("../../cmd/docsgen/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, cmd...)
	if len(files) == 0 {
		t.Fatal("no generator sources found")
	}
	// A network dial, a subprocess, or a model client. "net/http" itself is
	// allowed: the probe builds requests and serves them in process.
	forbidden := []string{
		`"net"`, "net.Dial", "http.Get(", `"os/exec"`, "anthropic", "openai", "api.anthropic.com",
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, bad := range forbidden {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q; the generator must not reach the network, a shell or a model", file, bad)
			}
		}
	}
}
