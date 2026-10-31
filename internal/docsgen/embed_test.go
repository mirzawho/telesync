package docsgen

import (
	"os"
	"path/filepath"
	"testing"
)

// The binary carries the documentation: EmbedDir copies docs/api under the
// embed package so `go build` folds it in, and the server reads it from there
// with no path to configure.
func TestEmbedDirMirrorsDocsAPIAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "api", "README.md"), "<!-- title: API -->\n# API\n")
	writeFile(t, filepath.Join(root, "docs", "api", "generated", "facts.json"), "{}\n")
	writeFile(t, filepath.Join(root, "docs", "api", "manual", "components", "errors.md"), "<!-- component: errors -->\nErrors.\n")

	target := filepath.Join(root, "internal", "docsite", "files")
	if err := Embed(root, target); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for _, rel := range []string{"README.md", "generated/facts.json", "manual/components/errors.md"} {
		want, err := os.ReadFile(filepath.Join(DocsDir(root), filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s was not copied: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s differs from docs/api", rel)
		}
	}
	// The placeholder that lets the package compile before the first copy
	// must not end up in the embedded tree.
	if _, err := os.Stat(filepath.Join(target, ".keep")); !os.IsNotExist(err) {
		t.Fatalf(".keep is still in the embedded tree: %v", err)
	}

	// A second run overwrites what changed and removes what was deleted.
	writeFile(t, filepath.Join(root, "docs", "api", "generated", "facts.json"), "{\"v\":2}\n")
	writeFile(t, filepath.Join(target, "stale.md"), "gone\n")
	if err := Embed(root, target); err != nil {
		t.Fatalf("second Embed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "generated", "facts.json"))
	if err != nil || string(got) != "{\"v\":2}\n" {
		t.Fatalf("facts.json was not refreshed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "stale.md")); !os.IsNotExist(err) {
		t.Fatalf("a deleted page survived the copy: %v", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
