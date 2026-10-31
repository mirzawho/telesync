// Package docsite embeds the generated documentation so the binary serves
// GET /docs with no docs_dir beside it.
//
// The tree under files/ is a copy of the repository's docs/api, written by
// `make docs` (or `go run ./cmd/docsgen --embed`) straight before the build.
// Whatever binary `make build` produces therefore carries the documentation
// that matched its source: no path to configure, nothing to find on disk.
//
// The only file here besides the copy is .keep, which lets the package compile
// before the first copy exists; the generator removes it.
package docsite

import (
	"embed"
	"io/fs"
)

//go:embed all:files
var content embed.FS

// FS is the documentation tree as it was at build time, rooted at the
// documentation directory itself (the top-level entries are README.md,
// generated/, manual/, …), not at docs/api.
func FS() fs.FS {
	sub, err := fs.Sub(content, "files")
	if err != nil {
		// The path is a compile-time constant inside the embedded tree, so
		// this can only fail if the package is broken, not at run time.
		panic(err)
	}
	return sub
}
