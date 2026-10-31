package gateway_test

import (
	"strings"
	"testing"
)

// The sidebar mirrors the tree: a directory is a foldable section, its pages
// and subdirectories sit under it one level deeper, and the page being read is
// marked. A flat list of file names is what this replaces.
func TestSidebarRendersTheNestedTree(t *testing.T) {
	site, _ := docsSite(t, map[string]string{
		"README.md":                      "<!-- title: Docs -->\n# Docs\n",
		"generated/local.md":             "<!-- generated -->\n# Local\n",
		"generated/local/channels.md":    "<!-- generated -->\n# Channels\n",
		"generated/local/channels-id.md": "<!-- generated -->\n# One channel\n",
		"generated/telegram.md":          "<!-- generated -->\n# Telegram\n",
	})
	nav := sidebar(site.get("GET", "/docs/generated/local/channels.md").Body.String())

	for _, want := range []string{
		// The section on the way to the page is open; a sibling stays folded.
		`<details class="nav-directory" open><summary>local</summary>`,
		`<details class="nav-directory" open><summary>generated</summary>`,
		`<a class="nav-link" href="/docs/generated/telegram.md">telegram</a>`,
		// The page is a leaf under its directory, marked, and by its name.
		`<span class="nav-link active">channels</span>`,
		`<a class="nav-link" href="/docs/generated/local/channels-id.md">channels-id</a>`,
	} {
		if !strings.Contains(nav, want) {
			t.Fatalf("the sidebar does not contain %q:\n%s", want, nav)
		}
	}
	if strings.Contains(nav, "channels.md<") || strings.Contains(nav, "class=\"group\"") {
		t.Fatalf("the sidebar still shows file names in a flat list:\n%s", nav)
	}
}

// A directory's own page is its heading's "Overview" link, not a page called
// README listed among its children.
func TestSidebarDirectoryPageIsTheOverview(t *testing.T) {
	site, _ := docsSite(t, map[string]string{
		"README.md":           "<!-- title: Docs -->\n# Docs\n",
		"generated/README.md": "<!-- generated -->\n# Generated\n",
		"generated/api.md":    "<!-- generated -->\n# API\n",
	})
	nav := sidebar(site.get("GET", "/docs/generated").Body.String())
	for _, want := range []string{
		`<span class="nav-link nav-overview active">Overview</span>`,
		`<a class="nav-link" href="/docs/generated/api.md">api</a>`,
	} {
		if !strings.Contains(nav, want) {
			t.Fatalf("the sidebar does not contain %q:\n%s", want, nav)
		}
	}
	if strings.Contains(nav, `<summary>README</summary>`) {
		t.Fatalf("the README is a section of its own:\n%s", nav)
	}
	// From another page, the same directory's Overview links there.
	home := sidebar(site.get("GET", "/docs").Body.String())
	if !strings.Contains(home, `<a class="nav-link nav-overview" href="/docs/generated">Overview</a>`) {
		t.Fatalf("the directory's Overview does not link to its page:\n%s", home)
	}
}

// The page being read is marked exactly once, and a page outside the tree (a
// 404) marks nothing.
func TestSidebarMarksOnlyTheCurrentPage(t *testing.T) {
	site, _ := docsSite(t, map[string]string{
		"README.md": "<!-- title: Docs -->\n# Docs\n",
		"a.md":      "<!-- generated -->\n# A\n",
		"b.md":      "<!-- generated -->\n# B\n",
	})
	if n := strings.Count(site.get("GET", "/docs/a.md").Body.String(), `class="nav-link active"`); n != 1 {
		t.Fatalf("%d active links on /docs/a.md, want 1", n)
	}
	if n := strings.Count(site.get("GET", "/docs/missing.md").Body.String(), `class="nav-link active"`); n != 0 {
		t.Fatalf("%d active links on a 404 page, want 0", n)
	}
}

// The breadcrumb is the path to the page, each directory linking to its page.
func TestDocsBreadcrumb(t *testing.T) {
	site, _ := docsSite(t, map[string]string{
		"README.md":                   "<!-- title: Docs -->\n# Docs\n",
		"generated/README.md":         "<!-- generated -->\n# Generated\n",
		"generated/local/README.md":   "<!-- generated -->\n# Local\n",
		"generated/local/channels.md": "<!-- generated -->\n# Channels\n",
	})
	page := site.get("GET", "/docs/generated/local/channels.md").Body.String()
	want := `<p class="breadcrumb"><a href="/docs">docs</a><span>/</span><a href="/docs/generated">generated</a><span>/</span><a href="/docs/generated/local">local</a><span>/</span><span class="here">channels</span></p>`
	if !strings.Contains(page, want) {
		var b strings.Builder
		for _, line := range strings.Split(page, "\n") {
			if strings.Contains(line, "breadcrumb") {
				b.WriteString(line + "\n")
			}
		}
		t.Fatalf("the breadcrumb is not\n%s\ngot\n%s", want, b.String())
	}
}

func sidebar(html string) string {
	start := strings.Index(html, "<nav>")
	end := strings.Index(html, "</nav>")
	if start < 0 || end < 0 {
		return html
	}
	return html[start : end+len("</nav>")]
}
