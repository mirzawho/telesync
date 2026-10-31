// Package panel embeds the admin panel's built browser bundle so the binary
// serves it at /admin with no Node runtime and nothing on disk beside it.
//
// The tree under files/ is a copy of web/apps/web/build/client, written by
// `make panel` (node web/scripts/build-panel.mjs) straight before `go build`
// folds it in. A build without that step has only .keep here and answers 503
// with instructions, rather than serving a stale or half-copied panel.
package panel

import (
	"embed"
	"io/fs"
)

//go:embed all:files
var content embed.FS

// Files is the panel's static tree as it was at build time: index.html at the
// root, assets/ and any public files beside it.
func Files() fs.FS {
	sub, err := fs.Sub(content, "files")
	if err != nil {
		// The path is a compile-time constant inside the embedded tree, so
		// this can only fail if the package is broken, not at run time.
		panic(err)
	}
	return sub
}
