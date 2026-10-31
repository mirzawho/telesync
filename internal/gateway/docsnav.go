package gateway

import (
	"html/template"
	"net/url"
	"sort"
	"strings"
)

// The sidebar: the documentation tree, as nested sections.
//
// A directory is a <details> section labelled by its path segment, and its
// pages and subdirectories sit inside it one level deeper, each section opened
// by an "Overview" link to the directory's own page. A page is labelled by its
// file name without .md, so a reader sees "channels", not
// "api-v1-telegram-key-channels.md". The section on the way to the page being
// read is open, so the tree shows where the reader is; the rest stay folded,
// which is what keeps a 60-page tree readable. No script is involved:
// <details> is the browser's own disclosure.

// navItem is one entry in the sidebar.
type navItem struct {
	Name   string
	Path   string
	Active bool
	Open   bool
	// Leaf is a document. A directory may be empty (no page under it yet) and
	// is still a section.
	Leaf     bool
	Children []navItem
}

// navTree builds the sidebar for the page at current from the list of every
// published document.
func navTree(docs []string, current string, base string) []navItem {
	type dir struct {
		seg      string
		path     []string
		pages    []navItem
		readme   bool
		children map[string]*dir
	}
	newDir := func(seg string, path []string) *dir {
		return &dir{seg: seg, path: path, children: map[string]*dir{}}
	}
	root := newDir("", nil)
	for _, doc := range docs {
		segs := strings.Split(doc, "/")
		cur := root
		for _, seg := range segs[:len(segs)-1] {
			next, ok := cur.children[seg]
			if !ok {
				next = newDir(seg, append(append([]string{}, cur.path...), seg))
				cur.children[seg] = next
			}
			cur = next
		}
		file := segs[len(segs)-1]
		if file == "README.md" && len(segs) > 1 {
			// The directory's own page is its heading's "Overview" link.
			cur.readme = true
			continue
		}
		name := strings.TrimSuffix(file, ".md")
		cur.pages = append(cur.pages, navItem{
			Name:   name,
			Path:   base + DocURL(doc),
			Active: doc == current,
			Leaf:   true,
		})
	}

	var build func(d *dir) navItem
	build = func(d *dir) navItem {
		// The heading opens the directory's page ("Overview"), and a directory
		// whose pages all belong to subdirectories links to its listing route.
		item := navItem{Name: d.seg, Path: listRoute(d.path)}
		sort.Slice(d.pages, func(i, j int) bool { return d.pages[i].Name < d.pages[j].Name })
		item.Children = append(item.Children, d.pages...)
		names := make([]string, 0, len(d.children))
		for name := range d.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			item.Children = append(item.Children, build(d.children[name]))
		}
		return item
	}
	items := build(root).Children
	openPath(items, current)
	return items
}

// listRoute is a directory's route: /docs/<path>. The site answers it with
// the directory's README when it has one, otherwise with a listing.
func listRoute(path []string) string {
	if len(path) == 0 {
		return ""
	}
	segs := make([]string, len(path))
	for i, p := range path {
		segs[i] = url.PathEscape(p)
	}
	return docsPath + "/" + strings.Join(segs, "/")
}

// openPath opens the sections that hold the page being read, so the reader
// sees where they are without unfolding anything.
func openPath(items []navItem, current string) {
	if current != "" {
		current = DocURL(current)
	}
	var holds func(items []navItem) bool
	holds = func(items []navItem) bool {
		for _, it := range items {
			if (it.Leaf && it.Path == current) || holds(it.Children) {
				return true
			}
		}
		return false
	}
	var open func(items []navItem)
	open = func(items []navItem) {
		for i := range items {
			if items[i].Leaf || !holds(items[i].Children) {
				continue
			}
			items[i].Open = true
			open(items[i].Children)
		}
	}
	open(items)
}

// navHTML renders the sidebar: the directory sections, and the page being
// read marked.
func navHTML(docs []string, current string, base string) template.HTML {
	active := ""
	if current != "" {
		active = base + DocURL(current)
	}
	var b strings.Builder
	b.WriteString(`<div class="nav-group">`)
	for _, it := range navTree(docs, current, base) {
		b.WriteString(renderNavItem(it, active))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String()) //nolint:gosec // renderNavItem escapes every value it writes
}

// renderNavItem is one entry: a document as a link, a directory as a <details>
// section that folds, with its "Overview" page first and then its pages and
// subdirectories.
func renderNavItem(it navItem, active string) string {
	label := template.HTMLEscapeString(it.Name)
	if it.Leaf {
		return navLink(it, label, active, "")
	}
	open := ""
	if it.Open {
		open = " open"
	}
	var b strings.Builder
	b.WriteString(`<details class="nav-directory"` + open + `><summary>` + label + `</summary><div class="nav-children">`)
	b.WriteString(navLink(navItem{Path: it.Path}, "Overview", active, "nav-overview"))
	for _, c := range it.Children {
		b.WriteString(renderNavItem(c, active))
	}
	b.WriteString(`</div></details>`)
	return b.String()
}

// navLink is one page in the sidebar; the page being read is marked and has no
// link to itself.
func navLink(it navItem, label, active, extra string) string {
	class := "nav-link"
	if extra != "" {
		class += " " + extra
	}
	href := template.HTMLEscapeString(it.Path)
	if it.Path == active {
		return `<span class="` + class + ` active">` + label + `</span>`
	}
	return `<a class="` + class + `" href="` + href + `">` + label + `</a>`
}
