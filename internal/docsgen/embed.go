package docsgen

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// EmbedDir is where docs/api is copied for the compiler to fold into the
// binary: the docsite package embeds that tree with //go:embed.
const EmbedDir = "internal/docsite/files"

// Embed makes the tree under embedDir an exact copy of repoRoot/docs/api, so
// the next `go build` carries the documentation that was just generated.
//
// The copy is a mirror, not a merge: a page deleted from docs/api is deleted
// from the copy too, or the binary would serve documentation the source no
// longer has. Nothing outside embedDir is touched.
func Embed(repoRoot, embedDir string) error {
	files, err := readTree(DocsDir(repoRoot))
	if err != nil {
		return fmt.Errorf("read the documentation: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("docs/api under %s holds no documentation", repoRoot)
	}
	for _, rel := range sortedKeys(files) {
		dst := filepath.Join(embedDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, files[rel], 0o644); err != nil {
			return fmt.Errorf("copy %s into %s: %w", rel, embedDir, err)
		}
	}

	// What the copy holds and docs/api no longer does: a deleted page, or the
	// .keep placeholder that let the package compile before the first copy.
	stale, err := listTree(embedDir)
	if err != nil {
		return err
	}
	for i := len(stale) - 1; i >= 0; i-- {
		rel := stale[i]
		if _, ok := files[rel]; ok {
			continue
		}
		dst := filepath.Join(embedDir, filepath.FromSlash(rel))
		// Files came last in the walk, so every child is already gone and a
		// directory left over was stale in its entirety. One still holding a
		// page that exists in docs/api stays.
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			if info, statErr := os.Stat(dst); statErr == nil && info.IsDir() {
				continue
			}
			return err
		}
	}
	return nil
}

// readTree reads every file under dir, keyed by its slash-separated path.
func readTree(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = body
		return nil
	})
	return out, err
}

// listTree lists every path under dir, parents before children, refusing a
// path that would leave dir.
func listTree(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || path.IsAbs(rel) {
			return fmt.Errorf("refuse to remove %q, which is not inside %s", rel, dir)
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
