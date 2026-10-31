package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsDirFindsTheCheckoutTree proves a configuration inside a checkout
// serves that checkout's docs/api without naming it: the default is the
// closest docs/api in the directory or a parent.
func TestDocsDirFindsTheCheckoutTree(t *testing.T) {
	checkout := t.TempDir()
	docs := filepath.Join(checkout, "docs", "api")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "README.md"), []byte("<!-- title: test -->\n# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(checkout, "config", "local")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := loadIn(t, inside)
	if got := cfg.API.DocsDir; got != docs {
		t.Fatalf("api.docs_dir = %q, want %q", got, docs)
	}
}

// TestDocsDirDefaultsToTheEmbeddedCopy proves the common case needs no
// configuration and no checkout: with docs_dir unset, the binary serves the
// documentation it was built with. A tree on disk beside the configuration
// still wins, so an operator can override it.
func TestDocsDirDefaultsToTheEmbeddedCopy(t *testing.T) {
	outside := t.TempDir()
	cfg := loadIn(t, outside)
	if got, want := cfg.API.DocsDir, "(embedded)"; got != want {
		t.Fatalf("api.docs_dir = %q, want %q", got, want)
	}

	// Beside the configuration, docs/api is the tree that is served.
	docs := filepath.Join(outside, "docs", "api")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg = loadIn(t, outside)
	if got := cfg.API.DocsDir; got != docs {
		t.Fatalf("api.docs_dir = %q, want the tree beside the configuration %q", got, docs)
	}
}

// TestDocsDirNamedMustExist keeps a typo a load error.
func TestDocsDirNamedMustExist(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nowhere")
	err := loadWith(t, dir, fmt.Sprintf(`,"docs_dir":%q`, missing))
	if err == nil {
		t.Fatal("a named directory that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("the error does not name the directory: %v", err)
	}
}

func loadIn(t *testing.T, dir string) Config {
	t.Helper()
	cfg, err := Load(writeConfig(t, dir, ""), hashVar)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadWith(t *testing.T, dir, apiExtra string) error {
	t.Helper()
	_, err := Load(writeConfig(t, dir, apiExtra), hashVar)
	return err
}

func writeConfig(t *testing.T, dir, apiExtra string) string {
	t.Helper()
	path := filepath.Join(dir, "telesync.json")
	body := fmt.Sprintf(`{"api":{"listen_addr":"127.0.0.1:8080"%s},"clients":[{"key":"official","type":"official","app_id":1,"app_hash_env":"HASH","phone_env":"PHONE","session_path":"%s","rpc_socket_path":"%s/run/o.sock"}]}`,
		apiExtra, filepath.Join(dir, "s.json"), dir)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hashVar(k string) string {
	if k == "HASH" {
		return "test-hash"
	}
	return ""
}
