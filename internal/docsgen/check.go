package docsgen

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mirza/telesync/internal/apidocs"
)

// Plan is what one pass would do to the tree.
type Plan struct {
	// Generated and Composed are the target content, by path relative to the
	// documentation root (docs/api).
	Generated map[string][]byte
	Composed  map[string][]byte
	Facts     *Facts
	// Created, Updated, Unchanged and Stale list the paths relative to
	// docs/api, sorted.
	Created   []string
	Updated   []string
	Unchanged []string
	Stale     []string
	// Legacy lists routes that appear only in the archived pages under
	// docs/api/legacy: they are served and documented there, but no generated
	// page names them.
	Legacy []string
	// Unreadable lists files that exist but could not be read.
	Unreadable []string
}

// Build runs one pass and compares it with the tree. It writes nothing.
func Build(ctx context.Context, opts Options) (*Plan, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	res, err := run(ctx, opts, logf)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Facts: res.Facts, Generated: map[string][]byte{}, Composed: map[string][]byte{}}
	for rel, body := range res.Generated {
		plan.Generated["generated/"+filepath.ToSlash(rel)] = body
	}
	for rel, body := range res.Manual {
		plan.Composed[filepath.ToSlash(rel)] = body
	}
	plan.compare(opts.RepoRoot)
	plan.Legacy = legacyRoutes(opts.RepoRoot, res.Generated)
	return plan, nil
}

// compare records what differs from what is on disk.
func (p *Plan) compare(root string) {
	docs := DocsDir(root)
	targets := map[string][]byte{}
	for rel, body := range p.Generated {
		targets[rel] = body
	}
	for rel, body := range p.Composed {
		targets[rel] = body
	}
	for rel, want := range targets {
		path := filepath.Join(docs, filepath.FromSlash(rel))
		have, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			p.Created = append(p.Created, rel)
		case err != nil:
			p.Failed(err, rel)
		case !bytes.Equal(have, want):
			p.Updated = append(p.Updated, rel)
		default:
			p.Unchanged = append(p.Unchanged, rel)
		}
	}
	// A generated file nobody renders any more is stale.
	genDir := GeneratedDir(root)
	_ = filepath.WalkDir(genDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(docs, path)
		rel = filepath.ToSlash(rel)
		if _, ok := p.Generated[rel]; !ok {
			p.Stale = append(p.Stale, rel)
		}
		return nil
	})
	sort.Strings(p.Created)
	sort.Strings(p.Updated)
	sort.Strings(p.Unchanged)
	sort.Strings(p.Stale)
}

// unreadable records a file that exists but cannot be read as a failure.
func (p *Plan) Failed(err error, rel string) {
	p.Unreadable = append(p.Unreadable, rel+": "+err.Error())
}

// Write applies the plan: generated files are written or removed, composed
// pages are rewritten, and nothing else is touched.
func (p *Plan) Write(root string) error {
	docs := DocsDir(root)
	for _, rel := range p.Created {
		if err := write(filepath.Join(docs, filepath.FromSlash(rel)), p.body(rel), true); err != nil {
			return err
		}
	}
	for _, rel := range p.Updated {
		if err := write(filepath.Join(docs, filepath.FromSlash(rel)), p.body(rel), true); err != nil {
			return err
		}
	}
	for _, rel := range p.Stale {
		if err := os.Remove(filepath.Join(docs, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return nil
}

// body is the target content of a path.
func (p *Plan) body(rel string) []byte {
	if b, ok := p.Generated[rel]; ok {
		return b
	}
	return p.Composed[rel]
}

// Outdated reports whether the tree needs a write.
func (p *Plan) Outdated() bool {
	return len(p.Created)+len(p.Updated)+len(p.Stale)+len(p.Unreadable) > 0
}

// Summary is the report printed after a run.
func (p *Plan) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "created %d, updated %d, unchanged %d, stale %d\n", len(p.Created), len(p.Updated), len(p.Unchanged), len(p.Stale))
	for _, rel := range p.Created {
		fmt.Fprintf(&b, "  + %s\n", rel)
	}
	for _, rel := range p.Updated {
		fmt.Fprintf(&b, "  ~ %s\n", rel)
	}
	for _, rel := range p.Stale {
		fmt.Fprintf(&b, "  - %s\n", rel)
	}
	for _, u := range p.Unreadable {
		fmt.Fprintf(&b, "  ! %s\n", u)
	}
	fmt.Fprintf(&b, "unresolved items: %d (docs/api/generated/unresolved.md)\n", len(p.Facts.Unresolved))
	if len(p.Legacy) > 0 {
		fmt.Fprintf(&b, "documented only in the archived pages (%d):\n", len(p.Legacy))
		for _, r := range p.Legacy {
			fmt.Fprintf(&b, "  %s\n", r)
		}
	}
	return b.String()
}

// write stores a page atomically. A generated page records in its first line
// that it is generated, so the renderer can refuse to serve one that was not.
func write(path string, body []byte, generated bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// legacyRoutes lists the served routes that the archived pages document and
// the generated pages do not.
func legacyRoutes(root string, generated map[string][]byte) []string {
	legacy, err := apidocs.DocRoutes(filepath.Join(DocsDir(root), "legacy"))
	if err != nil {
		return nil
	}
	covered := map[string]bool{}
	for rel, body := range generated {
		// The unresolved report names routes too, but it documents none.
		if !strings.HasSuffix(rel, ".md") || rel == "unresolved.md" {
			continue
		}
		for key := range routeRows(string(body)) {
			covered[key] = true
		}
	}
	var out []string
	for key := range legacy {
		method, path, _ := strings.Cut(key, " ")
		if !apidocs.Serves(method, path) || covered[key] {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

var routeRow = regexp.MustCompile(`(?m)^\| ([A-Z]+) \| ` + "`([^`]+)`" + ` \|`)

// routeRows reads the method and path of every route row in a page.
func routeRows(body string) map[string]bool {
	out := map[string]bool{}
	for _, m := range routeRow.FindAllStringSubmatch(body, -1) {
		out[m[1]+" "+m[2]] = true
	}
	return out
}
