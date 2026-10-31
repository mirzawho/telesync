package docsite

import (
	"io/fs"
	"testing"
)

// The tree is served as GET /docs, so it must hold the documentation and be
// rooted at the documentation directory, not at the repository.
func TestEmbeddedTreeIsTheDocumentationRoot(t *testing.T) {
	tree := FS()
	for _, want := range []string{"README.md", "generated", "manual"} {
		if _, err := fs.Stat(tree, want); err != nil {
			t.Fatalf("the embedded tree has no %s: %v (run make docs before building)", want, err)
		}
	}
	if _, err := fs.Stat(tree, "docs"); err == nil {
		t.Fatal("the tree is rooted at the repository, so every page would be under /docs/docs/")
	}
}
