package gateway

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// The documentation site: GET /docs and GET /docs/{path} render the Markdown
// files under one directory as HTML.
//
// The routes are public, like /healthz: the pages describe the API, not any
// account's data, and are meant to be read without a credential. What is
// published is an allow-list built at startup, so the endpoint exposes the
// documentation and nothing else under the root.

const (
	docsPath    = "/docs"
	docsMaxRead = 8 << 20
)

// NewDocsFS builds the handler for GET /docs and GET /docs/{path} over a
// filesystem: the tree embedded in the binary (docsite.FS), or a directory on
// disk.
//
// The set of documents is read once, at startup: what the listener can serve
// is fixed and known. A page is published when it declares a writer (see
// declaresPublisher), so a stray Markdown file in the tree is not exposed.
func NewDocsFS(fsys fs.FS) (http.Handler, error) {
	docs, err := listDocs(fsys)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, f := range docs {
		if admissable(f) && declaresPublisher(fsys, f) {
			allowed[f] = true
		}
	}
	if len(allowed) == 0 {
		return nil, errors.New("gateway: the documentation tree has no published page")
	}
	return &docsHandler{
		fsys:  fsys,
		files: allowed,
		md: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(html.WithHardWraps()),
		),
	}, nil
}

// listDocs lists every Markdown document in a tree, sorted.
func listDocs(fsys fs.FS) ([]string, error) {
	var out []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// docsHandler serves the documentation from a filesystem.
type docsHandler struct {
	fsys  fs.FS
	files map[string]bool
	md    goldmark.Markdown
	// base is the mount prefix the pages are linked under ("" at the root).
	base string
}

// SetBasePath makes the site's links absolute under a mount prefix, for a
// handler served beside the panel rather than at the listener's root.
func (h *docsHandler) SetBasePath(base string) {
	h.base = base
}

// MountedHandler is the documentation site with every link under base.
func MountedHandler(docs http.Handler, base string) http.Handler {
	if h, ok := docs.(*docsHandler); ok {
		h.SetBasePath(base)
	}
	return docs
}

// OsDocs is a filesystem-backed documentation root, for a deployment that
// overrides the embedded copy (api.docs_dir). Its paths are relative to dir,
// so nothing inside the tree can name a file outside it; a symlink is caught
// in read.
func OsDocs(dir string) fs.FS {
	return os.DirFS(dir)
}

// declaresPublisher reports whether a document is one the site serves.
//
// The documentation has two writers, and each signs its pages in the first
// lines: the generator writes "<!-- generated …" at the top of every page it
// owns, and the hand-written sources mark themselves "<!-- compose:…" or
// "<!-- manual …". A Markdown file that names neither is not published: the
// endpoint serves the documentation, not every file summary anyone leaves in
// the directory.
func declaresPublisher(fsys fs.FS, rel string) bool {
	f, err := fsys.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	text := string(head[:n])
	return strings.Contains(text, "<!-- generated") ||
		strings.Contains(text, "<!-- manual") ||
		strings.Contains(text, "<!-- compose:") ||
		// A composed page carries its title marker instead, and an archived
		// page says what it is: the generator writes both at the top.
		strings.Contains(text, "<!-- title:") ||
		strings.Contains(text, "<!-- archived:")
}

// admissable reports whether a document path may be served: a relative
// Markdown path with no empty, "." or ".." segment and no NUL.
func admissable(docPath string) bool {
	if docPath == "" || strings.ContainsRune(docPath, 0) {
		return false
	}
	if strings.HasPrefix(docPath, "/") || path.Clean(docPath) != docPath {
		return false
	}
	for _, seg := range strings.Split(docPath, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return strings.HasSuffix(docPath, ".md")
}

// docs serves GET /docs/... with the configured site. The gateway routes it
// before authentication, so it answers without a credential.
func (s *Server) docs(w http.ResponseWriter, r *http.Request, requestID string) {
	if s.cfg.Docs == nil {
		s.notFound(w, requestID)
		return
	}
	s.cfg.Docs.ServeHTTP(w, r)
}

// docsGroup reports whether a site path names a group of documents rather than
// a page: a directory that holds published pages and no README.md.
func (s *Server) docsGroup(p string) bool {
	h, ok := s.cfg.Docs.(*docsHandler)
	if !ok {
		return false
	}
	dir := strings.Trim(strings.TrimPrefix(p, docsPath), "/")
	if dir == "" || !admissable(dir+"/x.md") {
		return false
	}
	return h.hasPrefix(dir) && !h.files[dir+"/README.md"]
}

// route is one of the site's own paths, under the mount prefix.
func (h *docsHandler) route(p string) string { return h.base + p }

func (h *docsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeDocsError(w, http.StatusMethodNotAllowed, "Use GET to read the documentation.")
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, h.route(docsPath))
	rel = strings.TrimPrefix(rel, "/")

	// The landing page, and a directory path that names a document group, are
	// handled first: they take no file.
	if rel == "" || strings.HasSuffix(rel, "/") {
		h.landing(w, strings.Trim(rel, "/"))
		return
	}
	if rel == "README.md" {
		h.landing(w, "")
		return
	}
	// A directory without the trailing slash is the same page: a link the
	// sidebar builds for a group ("/docs/generated") must open it, not 404.
	if h.hasPrefix(rel) {
		h.landing(w, rel)
		return
	}
	if !admissable(rel) || !h.files[rel] {
		writeDocsError(w, http.StatusNotFound, "This page does not exist.")
		return
	}
	body, err := h.read(rel)
	if err != nil {
		writeDocsError(w, http.StatusNotFound, "This page does not exist.")
		return
	}
	h.render(w, rel, body)
}

// hasPrefix reports whether any published page sits under dir/.
func (h *docsHandler) hasPrefix(dir string) bool {
	for f := range h.files {
		if strings.HasPrefix(f, dir+"/") {
			return true
		}
	}
	return false
}

// landing serves a directory route: its README.md when it has one, otherwise a
// listing of the documents under it.
func (h *docsHandler) landing(w http.ResponseWriter, prefix string) {
	if prefix != "" && !admissable(prefix+"/x.md") {
		writeDocsError(w, http.StatusNotFound, "This page does not exist.")
		return
	}
	readme := "README.md"
	if prefix != "" {
		readme = prefix + "/README.md"
	}
	if h.files[readme] {
		if body, err := h.read(readme); err == nil {
			h.render(w, readme, body)
			return
		}
	}
	// Every page, not only this directory's: the sidebar shows the whole tree.
	var docs []string
	for f := range h.files {
		docs = append(docs, f)
	}
	sort.Strings(docs)
	h.renderList(w, prefix, docs)
}

// read reads a document. The path is checked twice: the allow-list was built
// from the same walk that lists the pages, and fs.FS refuses a path that
// leaves the root at all.
func (h *docsHandler) read(rel string) ([]byte, error) {
	if !fs.ValidPath(rel) {
		return nil, fs.ErrInvalid
	}
	f, err := h.fsys.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, docsMaxRead))
}

// render turns one document into a page.
func (h *docsHandler) render(w http.ResponseWriter, rel string, source []byte) {
	var out bytes.Buffer
	if err := h.md.Convert(rewriteDocLinks(rel, source, h.base), &out); err != nil {
		writeDocsError(w, http.StatusInternalServerError, "The page could not be rendered.")
		return
	}
	files := make([]string, 0, len(h.files))
	for f := range h.files {
		files = append(files, f)
	}
	sort.Strings(files)
	data := pageData{
		Title: pageTitle(source, rel),
		Path:  rel,
		//nolint:gosec // goldmark does not emit raw HTML, and every other value
		// the template receives is escaped by html/template.
		Body:       template.HTML(out.String()),
		Nav:        navHTML(files, rel, h.base),
		Home:       h.route(docsPath),
		Breadcrumb: crumb(rel, h.base),
		Crumb:      crumbName(rel),
	}
	writeHTML(w, data)
}

// renderList is the page for a directory with no README: the pages and
// subdirectories under it.
func (h *docsHandler) renderList(w http.ResponseWriter, prefix string, docs []string) {
	nested := navTree(docs, "", "")
	var children []navItem
	if prefix == "" {
		children = nested
	} else {
		children = findNav(nested, prefix)
	}
	var b strings.Builder
	if prefix != "" {
		b.WriteString(`<p><a href="` + h.route(docsPath) + `/">&larr; All documentation</a></p>` + "\n")
	}
	if len(children) == 0 {
		b.WriteString("<p>No documents here.</p>\n")
	} else {
		b.WriteString("<ul class=\"listing\">\n")
		for _, c := range children {
			href := c.Path
			name := c.Name
			if href == "" {
				// A subdirectory with no README: link to its route.
				href = DocURL(prefix + "/" + c.Name + "/x.md")
				name += "/"
			}
			b.WriteString(`<li><a href="` + href + `">` + template.HTMLEscapeString(name) + "</a></li>\n")
		}
		b.WriteString("</ul>\n")
	}
	data := pageData{
		Title: prefixTitle(prefix),
		Path:  prefix,
		//nolint:gosec // every part above is escaped or built from an escaped path
		Body:       template.HTML(b.String()),
		Nav:        navHTML(docs, "", h.base),
		Home:       h.route(docsPath),
		Breadcrumb: crumb(prefix, h.base),
		Crumb:      crumbName(prefix + "/"),
	}
	writeHTML(w, data)
}

func writeHTML(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// The pages run no script and load nothing remote: the policy states that
	// as enforced fact, not as a hope about the content.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_ = pageTemplate.Execute(w, data)
}

func writeDocsError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = errorTemplate.Execute(w, pageData{Title: http.StatusText(status), Body: template.HTML(template.HTMLEscapeString(message))}) //nolint:gosec // escaped in the same expression
}

// rewriteDocLinks makes relative links work over HTTP.
//
// A Markdown link to another document ("telegram/channels.md") becomes the
// route that serves it ("/docs/api/telegram/channels.md"), keeping any #anchor.
// A link to anything else under the root (a schema file, say) is left alone and
// the browser gets a 404. Absolute URLs, mailto: links and bare anchors are
// untouched. The rewrite works on the Markdown source, before rendering, so
// goldmark still escapes everything it emits.
func rewriteDocLinks(rel string, source []byte, base string) []byte {
	prefix := base
	dir := path.Dir(rel)
	out := linkPattern.ReplaceAllStringFunc(string(source), func(m string) string {
		sub := linkPattern.FindStringSubmatch(m)
		target := sub[1]
		if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") ||
			strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			return m
		}
		pathPart, anchor, hasAnchor := strings.Cut(target, "#")
		if pathPart == "" {
			return m
		}
		joined := path.Clean(path.Join(dir, pathPart))
		if strings.HasPrefix(joined, "..") || !strings.HasSuffix(joined, ".md") || !admissable(joined) {
			return m
		}
		rewritten := prefix + DocURL(joined)
		if hasAnchor {
			rewritten += "#" + anchor
		}
		return "](" + rewritten + ")"
	})
	return []byte(out)
}

// linkPattern captures the target of an inline Markdown link or image.
var linkPattern = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// pageTitle is the document's first heading, or its path.
func pageTitle(source []byte, rel string) string {
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return rel
}

func prefixTitle(prefix string) string {
	if prefix == "" {
		return "Documentation"
	}
	return "/" + prefix + "/"
}

// DocURL is the route of one documented page: /docs/<path>, escaped segment by
// segment, so a name with a space or a bracket is a link and never markup.
// A README.md is its directory's page, so its route is the directory's.
func DocURL(doc string) string {
	segs := strings.Split(doc, "/")
	if len(segs) > 1 && segs[len(segs)-1] == "README.md" {
		segs = segs[:len(segs)-1]
	}
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return docsPath + "/" + strings.Join(segs, "/")
}

// crumbPart is one step of the breadcrumb: a directory on the way to the page
// being read, each linking to that directory's page.
type crumbPart struct {
	Name string
	Path string
}

// crumb is the path to a page, directory by directory.
func crumb(rel, base string) []crumbPart {
	prefix := base
	segs := strings.Split(strings.TrimSuffix(rel, ".md"), "/")
	var out []crumbPart
	for i := 0; i < len(segs)-1; i++ {
		dir := strings.Join(segs[:i+1], "/")
		out = append(out, crumbPart{Name: segs[i], Path: prefix + DocURL(dir+"/README.md")})
	}
	return out
}

// crumbName is the last step: the page's name, without .md, or "overview" for
// a directory's own page.
func crumbName(rel string) string {
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	name := segs[len(segs)-1]
	switch name {
	case "", "README":
		return "overview"
	}
	return strings.TrimSuffix(name, ".md")
}

// findNav returns the children of the directory at path in the tree.
func findNav(items []navItem, path string) []navItem {
	for _, it := range items {
		if strings.HasSuffix(it.Path, DocURL(path+"/README.md")) {
			return it.Children
		}
		if found := findNav(it.Children, path); found != nil {
			return found
		}
	}
	return nil
}

// pageData is what the templates render.
type pageData struct {
	Title string
	Path  string
	Body  template.HTML
	// Nav is the sidebar, already rendered: the tree is recursive, which the
	// page template does not express.
	Nav        template.HTML
	Home       string
	Breadcrumb []crumbPart
	// Crumb is the last step of the breadcrumb: the page itself.
	Crumb string
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · telesync docs</title>
<style>
:root { color-scheme: light dark; --fg:#1d2430; --muted:#5b6575; --bg:#fbfbfc; --panel:#f0f2f5; --line:#dfe3e9; --link:#2156b3; --code:#eef1f5; }
@media (prefers-color-scheme: dark) { :root { --fg:#e6e9ef; --muted:#9aa4b2; --bg:#14171c; --panel:#1b1f26; --line:#2b313a; --link:#8ab4ff; --code:#222831; } }
* { box-sizing: border-box; }
body { margin:0; font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; color:var(--fg); background:var(--bg); }
.layout { display:flex; min-height:100vh; }
nav { width: 300px; flex: none; background:var(--panel); border-right:1px solid var(--line); padding:1.25rem 1rem 3rem; overflow:auto; position:sticky; top:0; height:100vh; font-size:.9rem; }
nav .brand { display:flex; align-items:center; gap:.5rem; margin:0 0 1.25rem; padding:0 .5rem; font-weight:600; letter-spacing:-.01em; }
nav .brand a { color:var(--fg); text-decoration:none; padding:0; }
.nav-group { margin:0 0 1rem; padding:0; }
.nav-group > p { margin:.25rem .5rem .35rem; font-size:.72rem; font-weight:600; text-transform:uppercase; letter-spacing:.08em; color:var(--muted); }
.nav-directory { margin:.15rem 0; }
.nav-directory > summary { cursor:pointer; display:flex; align-items:center; gap:.4rem; padding:.3rem .5rem; border-radius:5px; color:var(--fg); font-weight:500; list-style:none; }
.nav-directory > summary::-webkit-details-marker { display:none; }
.nav-directory > summary::before { content:""; width:.4rem; height:.4rem; flex:none; border-right:1.5px solid var(--muted); border-bottom:1.5px solid var(--muted); transform:rotate(-45deg); transition:transform .12s ease; }
.nav-directory[open] > summary::before { transform:rotate(45deg); }
.nav-directory > summary:hover { background:var(--line); }
.nav-children { margin:.1rem 0 .3rem; padding-left:.85rem; border-left:1px solid var(--line); margin-left:.75rem; }
nav a.nav-link { display:block; padding:.24rem .5rem; border-radius:5px; color:var(--muted); text-decoration:none; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
nav a.nav-link:hover { background:var(--line); color:var(--fg); }
nav a.nav-link.active { background:var(--link); color:#fff; font-weight:500; }
nav a.nav-overview { font-size:.8rem; font-style:italic; }
main { flex:1; min-width:0; padding:1.5rem 2.5rem 4rem; max-width: 980px; }
main h1 { line-height:1.25; margin-top:.5rem; }
main h2, main h3 { line-height:1.3; margin-top:2rem; }
a { color: var(--link); }
table { border-collapse:collapse; margin:1rem 0; display:block; overflow:auto; }
th, td { border:1px solid var(--line); padding:.3rem .6rem; text-align:left; vertical-align:top; }
th { background:var(--panel); }
code { background:var(--code); padding:.1em .3em; border-radius:3px; font-size:.9em; }
pre { background:var(--code); padding:.8rem 1rem; border-radius:6px; overflow:auto; }
pre code { background:none; padding:0; }
blockquote { border-left:3px solid var(--line); margin-left:0; padding-left:1rem; color:var(--muted); }
.breadcrumb { color:var(--muted); font-size:.875rem; margin:0 0 1.25rem; display:flex; flex-wrap:wrap; gap:.35rem; }
.breadcrumb a { color:var(--muted); text-decoration:none; }
.breadcrumb a:hover { color:var(--link); text-decoration:underline; }
.breadcrumb .here { color:var(--fg); }
@media (max-width: 820px) { .layout { display:block; } nav { width:auto; height:auto; position:static; border-right:0; border-bottom:1px solid var(--line); } main { padding:1rem; } }
</style>
</head>
<body>
<div class="layout">
<nav>
<p class="brand"><a href="{{.Home}}">telesync docs</a></p>
{{.Nav}}
</nav>
<main>
<p class="breadcrumb"><a href="{{.Home}}">docs</a>{{range .Breadcrumb}}<span>/</span><a href="{{.Path}}">{{.Name}}</a>{{end}}<span>/</span><span class="here">{{.Crumb}}</span></p>
{{.Body}}
</main>
</div>
</body>
</html>
`))

var errorTemplate = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · telesync docs</title>
<style>body{margin:0;font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;padding:2rem}a{color:#2156b3}</style>
</head><body><h1>{{.Title}}</h1><p>{{.Body}}</p><p><a href="/docs/">Back to the documentation</a></p></body></html>
`))
