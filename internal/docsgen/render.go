package docsgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Facts is everything the generator knows about the API.
//
// It is the JSON document written to generated/facts.json: the route table,
// the sources and rules read from the gateway's Go source, the probe and
// example observations, and the list of things that could not be established.
// The Markdown pages are rendered from this file alone, so the walk can be
// checked against the committed output without running anything.
type Facts struct {
	// Source notes where each kind of fact came from.
	Source map[string]string `json:"source"`
	Routes []RouteFacts      `json:"routes"`
	// Schemas are the request types, by Go type name.
	Schemas map[string]map[string]any `json:"schemas"`
	// Permissions lists the global permissions the gateway package knows.
	Permissions []string `json:"permissions"`
	// Unresolved lists what could not be established, with the evidence.
	Unresolved []Unresolved `json:"unresolved"`
}

// RouteFacts is one route and one method of the table.
type RouteFacts struct {
	Pattern string `json:"pattern"`
	Method  string `json:"method"`
	// Name is the Server method that serves it.
	Handler string `json:"handler"`
	// File and Line point at the ServeHTTP branch that routes to the handler.
	File string `json:"file"`
	Line int    `json:"line"`
	// Response is the value the handler passes to writeJSON.
	Response      string   `json:"response,omitempty"`
	StatusName    string   `json:"status_name,omitempty"`
	Status        int      `json:"status,omitempty"`
	RequestType   string   `json:"request_type,omitempty"`
	RequestSchema string   `json:"request_schema,omitempty"`
	Credentials   []string `json:"credentials,omitempty"`
	// CredentialCount is how many authorization conditions the source shows.
	CredentialCount int `json:"credential_count"`
	// Probe holds the probe-mode exchanges; Example the example snapshots.
	Probe   []Exchange `json:"probe,omitempty"`
	Example []Exchange `json:"example,omitempty"`
	// Notes are statements the source makes that the facts cannot confirm.
	Notes []string `json:"notes,omitempty"`
}

// Exchange is one observed request and what it got back.
type Exchange struct {
	Caller  string            `json:"caller"`
	Request string            `json:"request"`
	Status  int               `json:"status"`
	Code    string            `json:"code,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	JSON    bool              `json:"json"`
	// Body is the response document, written as raw JSON.
	Body json.RawMessage `json:"body,omitempty"`
}

// Unresolved is one thing the generator could not establish.
type Unresolved struct {
	Route    string   `json:"route"`
	What     string   `json:"what"`
	Evidence []string `json:"evidence,omitempty"`
}

// Schema names of the request types, keyed by Go type.
func (f *Facts) schemaFor(facts *RouteFacts) string {
	return facts.RequestSchema
}

// --- Markdown ---

// Group is one generated document group.
type Group struct {
	Slug  string
	Title string
	Intro string
}

// Groups are the document groups, in render order.
var Groups = []Group{
	{Slug: "identity", Title: "Identity and health",
		Intro: "`/healthz`, `/docs` and the caller's own session. These answer before any Telegram client is involved."},
	{Slug: "telegram", Title: "Telegram routes",
		Intro: "`/api/v1/telegram/{key}/...`: one configured Telegram client, selected by the client key."},
	{Slug: "local", Title: "Local routes",
		Intro: "`/api/v1/local/...`: this application's own data in SQLite, plus a local channel record that reaches its own Telegram client through that client's worker."},
}

// GroupOf reports the group a path belongs to.
func GroupOf(pattern string) string {
	switch {
	case strings.HasPrefix(pattern, "/api/v1/telegram/"):
		return "telegram"
	case strings.HasPrefix(pattern, "/api/v1/local/"):
		return "local"
	}
	return "identity"
}

// Render produces every page of the generated section, keyed by its path
// relative to the generated directory ("README.md", "schemas/user.json", …).
func Render(f *Facts) map[string][]byte {
	pages := map[string][]byte{}
	pages["facts.json"] = mustIndentJSON(f)
	pages["unresolved.md"] = renderUnresolved(f)
	for _, g := range Groups {
		var routes []RouteFacts
		for _, r := range f.Routes {
			if GroupOf(r.Pattern) == g.Slug {
				routes = append(routes, r)
			}
		}
		pages[g.Slug+".md"] = renderGroupOverview(g, routes)
		seen := map[string]bool{}
		var patterns []string
		for _, r := range routes {
			if !seen[r.Pattern] {
				seen[r.Pattern] = true
				patterns = append(patterns, r.Pattern)
			}
		}
		for _, pattern := range patterns {
			pages[g.Slug+"/"+docName(pattern)+".md"] = renderPattern(f, pattern, routes)
		}
	}
	for name, schema := range f.Schemas {
		raw, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			continue
		}
		pages["schemas/"+strings.ToLower(name)+".json"] = append(raw, '\n')
	}
	pages["schemas/README.md"] = []byte(renderSchemasIndex(f))
	return pages
}

// renderGroupOverview is one group's page: every route at a glance, and a link
// to the page of each pattern.
func renderGroupOverview(g Group, routes []RouteFacts) []byte {
	var b strings.Builder
	b.WriteString("<!-- generated by go run ./cmd/docsgen -->\n")
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", g.Title, g.Intro)
	b.WriteString(marker())
	b.WriteString("\n\n## Routes\n\n")
	b.WriteString("| Method | Path | Source | Success | Responses observed | Document |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range routes {
		success := "—"
		if r.Status != 0 {
			success = fmt.Sprintf("`%d`", r.Status)
		} else if r.StatusName != "" {
			success = "`" + r.StatusName + "`"
		}
		observed := "—"
		if len(r.Probe) > 0 || len(r.Example) > 0 {
			observed = fmt.Sprintf("%d", len(r.Probe)+len(r.Example))
		}
		fmt.Fprintf(&b, "| %s | `%s` | `Server.%s` | %s | %s | [%s](%s/%s.md) |\n",
			r.Method, r.Pattern, r.Handler, success, observed,
			docName(r.Pattern), g.Slug, docName(r.Pattern))
	}
	return []byte(b.String())
}

// marker is the first line of every generated page. The documentation handler
// publishes a page only when it finds this line (or a manual page's own
// marker), so it is also what makes a generated page reachable at /docs.
func marker() string {
	return "> Generated from the source by `go run ./cmd/docsgen`. Every route, method, type, status and response below was read from the code or observed from the gateway; the pages under `docs/api/manual/` explain what a generator cannot."
}

// mustIndentJSON writes v as indented JSON. A value that cannot be encoded is
// an error the caller must see, not an empty document: a facts file that
// silently came out as {} would pass every check and lose every fact.
func mustIndentJSON(v any) []byte {
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		panic("docsgen: encode facts: " + err.Error())
	}
	return append(raw, '\n')
}

// docName renders a pattern as a file name: "api-v1-local-users-{id}.md".
func docName(pattern string) string {
	name := strings.Trim(pattern, "/")
	name = strings.NewReplacer("/", "-", "{", "", "}", "", "_", "-").Replace(name)
	if name == "" {
		name = "root"
	}
	return name
}

// routeAnchor is the per-route heading id within a pattern page.
// renderPattern renders one route's page: a method per section.
func renderPattern(f *Facts, pattern string, routes []RouteFacts) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- generated by go run ./cmd/docsgen -->\n# `%s`\n\n%s\n\n", pattern, marker())
	b.WriteString("## Routes\n\n")
	b.WriteString("| Method | Path | Auth conditions | Success |\n| --- | --- | --- | --- |\n")
	for _, r := range routes {
		if r.Pattern != pattern {
			continue
		}
		auth := "—"
		if len(r.Credentials) > 0 {
			auth = fmt.Sprintf("%d", len(r.Credentials))
		} else if r.Handler == "login" || r.Handler == "gatewayHealth" {
			auth = "none"
		}
		success := "—"
		if r.Status != 0 {
			success = fmt.Sprintf("`%d`", r.Status)
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", r.Method, r.Pattern, auth, success)
	}
	b.WriteString("\n")
	for _, r := range routes {
		if r.Pattern != pattern {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", r.Method)
		fmt.Fprintf(&b, "- **Handler:** `Server.%s` (%s)\n", r.Handler, r.File)
		if r.Response != "" {
			status := r.StatusName
			if r.Status != 0 {
				status = fmt.Sprintf("%s (%d)", r.StatusName, r.Status)
			}
			fmt.Fprintf(&b, "- **Success:** `writeJSON` with `%s`, status `%s`\n", r.Response, status)
		}
		if r.RequestType != "" {
			fmt.Fprintf(&b, "- **Request body:** `%s` ([schema](../%s))\n", r.RequestType, r.RequestSchema)
		}
		for _, name := range componentOf(r.Pattern) {
			fmt.Fprintf(&b, "- **Documentation:** [%s](%s)\n", name, routeURL("manual/components/"+name+".md"))
		}
		if len(r.Credentials) > 0 {
			b.WriteString("\n### Authorization conditions in the source\n\n")
			for _, c := range r.Credentials {
				fmt.Fprintf(&b, "- `%s`\n", c)
			}
			b.WriteString("\nWhat these mean for who may call the route is in the authentication paragraph; the conditions are checked, not interpreted.\n")
		}
		if len(r.Probe) > 0 {
			b.WriteString("\n### Responses observed with an invalid probe value\n\n")
			b.WriteString("Each request below sent `not-a-valid-value` where the path has a variable, so a handler's validation answers. These are the actual statuses and bodies.\n\n")
			b.WriteString("| Caller | Request | Status | Shape |\n| --- | --- | --- | --- |\n")
			for _, p := range r.Probe {
				fmt.Fprintf(&b, "| %s | `%s` | %d | %s |\n", p.Caller, p.Request, p.Status, shape(p))
			}
			for _, p := range r.Probe {
				if len(p.Body) == 0 {
					continue
				}
				fmt.Fprintf(&b, "\n<details><summary>%s: %s → %d</summary>\n\n```json\n%s\n```\n\n</details>\n", p.Caller, p.Request, p.Status, p.Body)
			}
		}
		if len(r.Example) > 0 {
			b.WriteString("\n### Responses captured from the documentation examples\n\n")
			for _, e := range r.Example {
				fmt.Fprintf(&b, "- `%s` as %s → `%d`\n", e.Request, e.Caller, e.Status)
			}
			for _, e := range r.Example {
				if len(e.Body) == 0 {
					continue
				}
				fmt.Fprintf(&b, "\n<details><summary>%s → %d</summary>\n\n```json\n%s\n```\n\n</details>\n", e.Request, e.Status, e.Body)
			}
		}
	}
	return []byte(b.String())
}

// Components are the hand-written paragraphs, one per topic, in the order the
// composed pages use them. Each is docs/api/manual/components/<Name>.md.
var Components = []string{"authentication", "errors", "pagination", "health-and-docs", "telegram", "channels", "categories", "users", "wordpress", "publishing", "dashboard", "automation"}

// componentOf names the paragraphs a route needs. The generator cannot read
// prose, so the mapping is stated once, here, next to the other facts about
// paths.
func componentOf(pattern string) []string {
	switch {
	case pattern == "/healthz" || strings.HasPrefix(pattern, "/docs"):
		return []string{"health-and-docs"}
	case strings.HasPrefix(pattern, "/api/v1/auth/"):
		return []string{"authentication"}
	case strings.HasPrefix(pattern, "/api/v1/telegram/"):
		// Only a route that writes a message belongs to the publishing
		// component: the copy route and the direct send. A send's status is a
		// read and belongs to the Telegram component alone.
		if strings.HasSuffix(pattern, "/messages") || strings.HasSuffix(pattern, "/messages/send") {
			return []string{"telegram", "publishing"}
		}
		return []string{"telegram"}
	case strings.HasPrefix(pattern, "/api/v1/local/channels"):
		return []string{"channels"}
	case strings.HasPrefix(pattern, "/api/v1/local/categories"):
		return []string{"categories"}
	case strings.HasPrefix(pattern, "/api/v1/local/users"):
		return []string{"users"}
	case strings.HasPrefix(pattern, "/api/v1/local/wordpress"):
		if strings.HasSuffix(pattern, "/posts") {
			return []string{"wordpress", "publishing"}
		}
		return []string{"wordpress"}
	case pattern == "/api/v1/local/stats/dashboard":
		return []string{"dashboard"}
	case strings.HasPrefix(pattern, "/api/v1/local/automation/"):
		return []string{"automation"}
	}
	return nil
}

// routeURL is the handler's route for a documentation path, restated here:
// docsgen does not import the gateway's server code, and the route is a
// convention of the documentation tree, not behaviour. A generated page links
// to it with the path back to the documentation root, so the same link works
// on disk and over HTTP.
func routeURL(doc string) string {
	segs := strings.Split(doc, "/")
	if len(segs) > 1 && segs[len(segs)-1] == "README.md" {
		segs = segs[:len(segs)-1]
	}
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return "../../" + strings.Join(segs, "/")
}

// shape describes an exchange briefly for a table.
func shape(e Exchange) string {
	if !e.JSON {
		if ct := e.Headers["Content-Type"]; ct != "" {
			return ct
		}
		return "non-JSON"
	}
	if len(e.Body) > 0 {
		if e.Code != "" {
			return "`" + e.Code + "`"
		}
		return "JSON (below)"
	}
	return "non-JSON"
}

func renderSchemasIndex(f *Facts) string {
	var b strings.Builder
	b.WriteString("<!-- generated by go run ./cmd/docsgen -->\n")
	b.WriteString("# Request and response schemas\n\n")
	b.WriteString(marker())
	b.WriteString("\n\nThese JSON Schema documents are rendered from the request types in `internal/gateway`. `go_type` keeps the Go field type next to the JSON one; `required` lists fields whose tag has no `omitempty`.\n\n")
	b.WriteString("| Go type | Schema |\n| --- | --- |\n")
	names := make([]string, 0, len(f.Schemas))
	for name := range f.Schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "| `%s` | [%s.json](%s.json) |\n", name, strings.ToLower(name), strings.ToLower(name))
	}
	return b.String()
}

func renderUnresolved(f *Facts) []byte {
	var b strings.Builder
	b.WriteString("<!-- generated by go run ./cmd/docsgen -->\n")
	b.WriteString("# Unresolved extraction items\n\n")
	b.WriteString(marker())
	b.WriteString("\n\nEverything below could not be established from the source by the generator. Nothing here is guessed; each item is either described by hand under `docs/api/manual/` or still needs a decision.\n\n")
	if len(f.Unresolved) == 0 {
		b.WriteString("None: every route and schema was established from the source.\n")
		return []byte(b.String())
	}
	for _, u := range f.Unresolved {
		fmt.Fprintf(&b, "## `%s`\n\n%s\n\n", u.Route, u.What)
		for _, e := range u.Evidence {
			fmt.Fprintf(&b, "- `%s`\n", e)
		}
	}
	return []byte(b.String())
}

// ComposePage renders a composed page: the manual parts around the generated
// endpoint sections, in the order the manual file lists them.
func ComposePage(title, before string, routes []RouteFacts, after string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n", title)
	b.WriteString(region("before"))
	b.WriteString("\n")
	b.WriteString(before)
	b.WriteString("\n")
	for i, r := range routes {
		b.WriteString(region(fmt.Sprintf("route:%s:%d", r.Method, i)))
		b.WriteString("\n")
		b.WriteString(renderFactsSection(r))
		b.WriteString("\n")
	}
	b.WriteString(region("after"))
	b.WriteString("\n")
	b.WriteString(after)
	b.WriteString("\n")
	return b.Bytes()
}

// renderFactsSection is one endpoint's generated half.
func renderFactsSection(r RouteFacts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s `%s`\n\n", r.Method, r.Pattern)
	fmt.Fprintf(&b, "- Handler: `Server.%s` (%s)\n", r.Handler, r.File)
	if r.Response != "" {
		fmt.Fprintf(&b, "- Success: `writeJSON` `%s`, status `%s`\n", r.Response, r.StatusName)
	}
	if r.RequestType != "" {
		fmt.Fprintf(&b, "- Request body: `%s` ([schema](schemas/%s.json))\n", r.RequestType, strings.ToLower(r.RequestType))
	}
	for _, c := range r.Credentials {
		fmt.Fprintf(&b, "- Authorization condition: `%s`\n", c)
	}
	return b.String()
}

func region(name string) string {
	return fmt.Sprintf("<!-- generated:begin %s -->\n<!-- generated:end %s -->\n", name, name)
}
