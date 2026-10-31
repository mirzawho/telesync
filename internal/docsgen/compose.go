package docsgen

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The manual side of the documentation.
//
// A page under docs/api/manual, or docs/api/README.md, that is not composed is
// a hand-written page in its own right: it starts with "<!-- title: … -->" (or
// "<!-- archived: … -->"), and the generator copies it into docs/api at the
// same path, byte for byte except for that marker.
//
// Composing a page means putting generated sections into it:
//
//	<!-- title: API overview -->
//	# API overview
//	…prose…
//	<!-- endpoints: /healthz GET, /api/v1/auth/me GET -->
//	<!-- endpoints:end -->
//	…prose…
//	<!-- components: authentication, errors -->
//	<!-- components:end -->
//
// The sections between the markers are rewritten on every run from the facts;
// everything outside them is the hand-written page and is never touched.

var (
	titleMarker       = regexp.MustCompile(`^<!-- title: (.*?) -->\s*$`)
	archivedMarker    = regexp.MustCompile(`^<!-- archived: .*? -->\s*$`)
	endpointsSection  = regexp.MustCompile(`(?s)<!-- endpoints:(.*?)-->\n.*?<!-- endpoints:end -->`)
	componentsSection = regexp.MustCompile(`(?s)<!-- components:(.*?)-->\n.*?<!-- components:end -->`)
	componentMarker   = regexp.MustCompile(`(?m)^<!-- component: .*? -->\s*$`)
)

// manualPage is one hand-written file and where it belongs under docs/api.
type manualPage struct {
	// source is the path relative to docs/api; "" is docs/api/README.md.
	source string
	title  string
	body   string
}

// readManual reads every hand-written page: docs/api/README.md, everything
// under docs/api/manual except the components (which are inserted into pages)
// and the manual index, and the archived tree as it stands.
func readManual(root string) ([]manualPage, []string, error) {
	var pages []manualPage
	var problems []string
	if body, err := os.ReadFile(filepath.Join(DocsDir(root), "README.md")); err == nil {
		if m := titleMarker.FindSubmatch(body); m != nil {
			pages = append(pages, manualPage{source: "README.md", title: string(m[1]), body: string(body)})
		}
	}

	manualDir := ManualDir(root)
	err := filepath.WalkDir(manualDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		rel, err := filepath.Rel(manualDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "README.md" || strings.HasPrefix(rel, "components/") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(rel, "legacy/"):
			// The archive is published where it already points: its links are
			// relative to docs/api/manual/legacy, so its pages land under
			// docs/api/manual/legacy as well.
			pages = append(pages, manualPage{source: "manual/legacy/" + strings.TrimPrefix(rel, "legacy/"), title: titleOf(body), body: string(body)})
		case startsWithMarker(body, titleMarker):
			pages = append(pages, manualPage{source: rel, title: titleOf(body), body: string(body)})
		case startsWithMarker(body, archivedMarker):
			pages = append(pages, manualPage{source: rel, title: titleOf(body), body: string(body)})
		default:
			problems = append(problems, rel+": no <!-- title: … --> or <!-- archived: … --> marker; not published")
		}
		return nil
	})
	sort.Slice(pages, func(i, j int) bool { return pages[i].source < pages[j].source })
	return pages, problems, err
}

// startsWithMarker reports whether the first line of a page is the marker: the
// marker is an instruction at the top of the file, not a line to find.
func startsWithMarker(body []byte, marker *regexp.Regexp) bool {
	first, _, _ := strings.Cut(strings.TrimPrefix(string(body), "\ufeff"), "\n")
	return marker.MatchString(strings.TrimSpace(first))
}

// titleOf is the first Markdown heading of a page.
func titleOf(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

// composePage renders one hand-written page: its generated sections from the
// facts and the components, the rest as written. The title marker is an
// instruction to the generator and is dropped; an archived page keeps its own.
func composePage(page manualPage, facts *Facts, components map[string]string) []byte {
	body := endpointsSection.ReplaceAllStringFunc(page.body, func(section string) string {
		return renderEndpoints(facts, splitList(endpointsSection.FindStringSubmatch(section)[1]))
	})
	body = componentsSection.ReplaceAllStringFunc(body, func(section string) string {
		return renderComponents(splitList(componentsSection.FindStringSubmatch(section)[1]), components)
	})
	lines := strings.Split(body, "\n")
	var kept []string
	for _, line := range lines {
		if titleMarker.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return []byte(strings.Join(kept, "\n"))
}

// splitList reads a comma-separated marker value.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// readComponents reads docs/api/manual/components into name → Markdown, with
// the component marker removed.
func readComponents(root string) (map[string]string, error) {
	out := map[string]string{}
	dir := filepath.Join(ManualDir(root), "components")
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[strings.TrimSuffix(filepath.Base(path), ".md")] =
			strings.TrimSpace(componentMarker.ReplaceAllString(string(body), "")) + "\n"
		return nil
	})
	if os.IsNotExist(err) {
		return out, nil
	}
	return out, err
}

// renderComponents places the named components, in order.
func renderComponents(names []string, components map[string]string) string {
	var b strings.Builder
	for _, name := range names {
		body, ok := components[name]
		if !ok {
			continue
		}
		b.WriteString("\n---\n\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// renderEndpoints is the generated half of an overview: one row and one
// summary per route named in the marker, in the order given.
func renderEndpoints(facts *Facts, routes []string) string {
	var b strings.Builder
	b.WriteString("<!-- endpoints: ")
	b.WriteString(strings.Join(routes, ", "))
	b.WriteString(" -->\n")
	b.WriteString("| Method | Path | Handler | Success | Documentation |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, key := range routes {
		method, pattern, ok := strings.Cut(key, " ")
		r, found := facts.route(method, pattern)
		if !ok || !found {
			continue
		}
		success := "—"
		if r.Status != 0 {
			success = "`" + strconv.Itoa(r.Status) + "`"
		}
		group := GroupOf(pattern)
		b.WriteString("| " + method + " | `" + pattern + "` | `Server." + r.Handler + "` | " +
			success + " | [" + docName(pattern) + "](" + group + "/" + docName(pattern) + ".md) |\n")
	}
	for _, key := range routes {
		method, pattern, ok := strings.Cut(key, " ")
		r, found := facts.route(method, pattern)
		if !ok || !found {
			continue
		}
		b.WriteString("\n")
		b.WriteString(renderFactsSection(r))
	}
	b.WriteString("<!-- endpoints:end -->")
	return b.String()
}

// route finds one method of one pattern in the facts.
func (f *Facts) route(method, pattern string) (RouteFacts, bool) {
	for _, r := range f.Routes {
		if r.Method == method && r.Pattern == pattern {
			return r, true
		}
	}
	return RouteFacts{}, false
}
