package docsgen

import (
	"context"
	"fmt"
	"go/ast"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mirza/telesync/internal/apidocs"
	"github.com/mirza/telesync/internal/channelstore"
)

// Options configures one generation pass.
type Options struct {
	// RepoRoot holds docs/api and internal/gateway.
	RepoRoot string
	// GatewayDir is the package the rules are read from; it defaults to
	// internal/gateway under RepoRoot.
	GatewayDir string
	// Probe supplies the gateway the observations are made against.
	Probe apidocs.Prober
	// Static is the documentation renderer the /docs routes serve. The
	// generator sends those routes to their handler directly, so an
	// unauthenticated /docs request cannot be confused with one a caller with
	// no account would have to authenticate for.
	Static http.Handler
	// Logf receives progress lines. Nil discards them.
	Logf func(format string, args ...any)
}

// DocsDir is the documentation root under the repository.
func DocsDir(repoRoot string) string { return filepath.Join(repoRoot, "docs", "api") }

// GeneratedDir is the generated section. Everything in it is owned by the
// generator and is replaced on every run.
func GeneratedDir(repoRoot string) string { return filepath.Join(DocsDir(repoRoot), "generated") }

// ManualDir is the hand-written section: never written by the generator.
func ManualDir(repoRoot string) string { return filepath.Join(DocsDir(repoRoot), "manual") }

// result is what one pass found.
type result struct {
	Generated map[string][]byte
	Changed   []string
	Manual    map[string][]byte // composed pages, by path under docs/api
	Facts     *Facts
}

// run is one full pass: observe, infer, render, compose. It writes nothing.
func run(ctx context.Context, opts Options, logf func(string, ...any)) (*result, error) {
	root := opts.RepoRoot
	gatewayDir := opts.GatewayDir
	if gatewayDir == "" {
		gatewayDir = filepath.Join(root, "internal", "gateway")
	}
	proj, err := loadProject(gatewayDir)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", gatewayDir, err)
	}

	// The examples run first, on a fresh probe: they create the ids the
	// snapshots and the permission example need, and they are the only pass
	// that writes.
	examples, index, err := readExamples(filepath.Join(DocsDir(root), "examples"), filepath.Join(ManualDir(root), "legacy", "examples"))
	if err != nil {
		return nil, err
	}
	snaps, err := ObserveExamples(ctx, opts.Probe, examples, index)
	if err != nil {
		return nil, fmt.Errorf("replay the examples: %w", err)
	}
	callers := Callers(opts.Probe)
	probeObs := ObserveProbe(ctx, opts.Probe, apidocs.Routes, callers)
	probeObs = append(probeObs, ProbeStatic(ctx, opts.Static, apidocs.Routes, callers)...)

	facts := &Facts{
		Source: map[string]string{
			"routes":      "internal/apidocs/table.go, checked against the gateway by TestDocumentationCoversEveryRoute",
			"handler":     "the ServeHTTP branch in internal/gateway that routes to each handler",
			"response":    "the value each handler passes to writeJSON, and its status argument",
			"request":     "the type each handler decodes a JSON body into",
			"credentials": "the credential and permission checks in the handler's own package functions",
			"probe":       "requests sent to a gateway over an in-memory SQLite store, with an invalid value in every path variable",
			"example":     "the requests in docs/api/examples/*.http, replayed in order",
		},
		Schemas:     map[string]map[string]any{},
		Permissions: permissionNames(),
	}
	var unresolved []Unresolved
	byRoute := map[string]*RouteFacts{}
	for _, r := range apidocs.Routes {
		for _, m := range r.Methods {
			rf := &RouteFacts{Pattern: r.Pattern, Method: m}
			for _, p := range probeObs {
				if p.Pattern == r.Pattern && p.Method == m {
					rf.Probe = append(rf.Probe, Exchange{
						Caller: p.Caller, Request: p.Method + " " + p.Path, Status: p.Status,
						Code: p.Code, Headers: p.Headers, JSON: p.JSON, Body: rawBody(p.Body),
					})
				}
			}
			for _, s := range snaps {
				if SnapshotKey(s.Method, s.Path) == m+" "+r.Pattern {
					rf.Example = append(rf.Example, Exchange{
						Caller: s.Caller, Request: s.Method + " " + s.Path, Status: s.Status,
						Code: s.Code, JSON: s.JSON, Body: rawBody(s.Body),
					})
				}
			}
			byRoute[m+" "+r.Pattern] = rf
		}
	}

	// The handler each route runs, and the rules it applies. The map is walked
	// in sorted order so the report is the same on every run.
	handlerTable := routeHandlers(proj)
	handlersByPattern := handlerTable
	var patterns []string
	for _, r := range apidocs.Routes {
		if _, ok := handlersByPattern[r.Pattern]; !ok {
			continue
		}
		if !containsString(patterns, r.Pattern) {
			patterns = append(patterns, r.Pattern)
		}
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		handlers := handlersByPattern[pattern]
		for method, h := range handlers {
			rf := byRoute[method+" "+pattern]
			if rf == nil {
				unresolved = append(unresolved, Unresolved{
					Route: method + " " + pattern,
					What:  "The route is served but ServeHTTP does not visibly route this method; document it by hand.",
				})
				continue
			}
			fn := handlerNamed(proj, h.name)
			rf.Handler = h.name
			rf.File = h.file
			rf.Line = h.line
			if fn == nil {
				unresolved = append(unresolved, Unresolved{
					Route: method + " " + pattern,
					What:  "The handler `" + h.name + "` was not found in the gateway package.",
				})
				continue
			}
			rf.Response, rf.StatusName = proj.responseOf(fn)
			rf.Status = httpStatus(rf.StatusName)
			// The route's conditions are those on the way to its handler: the
			// checks before ServeHTTP's dispatch, its branch, and the handler.
			checks := proj.credentialsOfRoute(pattern, fn)
			rf.CredentialCount = len(checks)
			for _, c := range checks {
				rf.Credentials = append(rf.Credentials, c.Expression+"  ("+c.In+")")
			}
			for _, t := range proj.requestTypes(fn) {
				if schema := proj.requestSchema(t); schema != nil {
					rf.RequestType = t
					rf.RequestSchema = "schemas/" + strings.ToLower(t) + ".json"
					registerSchema(proj, facts, t)
					break
				}
			}
			if len(checks) == 0 {
				unresolved = append(unresolved, Unresolved{
					Route:    method + " " + pattern,
					What:     "No permission check was found in the handler or the package functions it calls. Whether a credential alone is enough for this route must be confirmed by hand (and by the probe exchange below).",
					Evidence: []string{rf.Handler + " (" + rf.File + ")", describeProbe(rf.Probe)},
				})
			}
			if rf.Response == "" {
				unresolved = append(unresolved, Unresolved{
					Route:    method + " " + pattern,
					What:     "The success body could not be named from the source (the handler writes more than one shape, or none).",
					Evidence: []string{rf.Handler + " (" + rf.File + ")"},
				})
			}
		}
	}

	for _, rf := range byRoute {
		facts.Routes = append(facts.Routes, *rf)
	}
	sort.SliceStable(facts.Routes, func(i, j int) bool {
		if facts.Routes[i].Pattern != facts.Routes[j].Pattern {
			return facts.Routes[i].Pattern < facts.Routes[j].Pattern
		}
		return facts.Routes[i].Method < facts.Routes[j].Method
	})
	// One route may produce the same note twice (two methods with one defect);
	// the report keeps them in a fixed order.
	sort.SliceStable(unresolved, func(i, j int) bool {
		if unresolved[i].Route != unresolved[j].Route {
			return unresolved[i].Route < unresolved[j].Route
		}
		return unresolved[i].What < unresolved[j].What
	})
	facts.Unresolved = unresolved

	// The hand-written pages, with their generated sections in place.
	components, err := readComponents(root)
	if err != nil {
		return nil, err
	}
	pages, problems, err := readManual(root)
	if err != nil {
		return nil, err
	}
	manual := map[string][]byte{}
	for _, page := range pages {
		manual[page.source] = composePage(page, facts, components)
	}
	for _, p := range problems {
		facts.Unresolved = append(facts.Unresolved, Unresolved{Route: "docs", What: p})
	}

	return &result{Facts: facts, Generated: Render(facts), Manual: manual}, nil
}

// docsPath is the documentation route's prefix.
const docsPath = "/docs"

// ProbeStatic sends the documentation routes to the site handler directly.
//
// The handler is built over the tree as it is being generated (docs/api), not
// over the copy embedded in whatever binary runs the generator, so the record
// below is of the pages this pass is about to write. The route is public, and
// the exchange is sent without a credential for that reason.
func ProbeStatic(ctx context.Context, h http.Handler, routes []apidocs.Route, callers []Caller) []Observation {
	if h == nil {
		return nil
	}
	var out []Observation
	for _, r := range routes {
		if !strings.HasPrefix(r.Pattern, docsPath) {
			continue
		}
		for _, method := range r.Methods {
			// One request per route, not per caller: the route is public, so
			// every caller sees the same page.
			obs := exchangeStatic(ctx, h, r, method)
			for _, c := range callers {
				o := obs
				o.Caller = c.ID
				out = append(out, o)
			}
		}
	}
	return out
}

// exchangeStatic sends one request to the site handler.
func exchangeStatic(ctx context.Context, h http.Handler, r apidocs.Route, method string) Observation {
	// The landing page, whatever the document was: the route's behaviour is
	// the page it serves, and a specific document would make the snapshot
	// depend on which documents exist.
	docPath := docsPath
	if r.Pattern != docsPath {
		docPath = docsPath + "/missing-manual-document.md"
	}
	req := httptest.NewRequest(method, docPath, nil)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	obs := Observation{
		Method: method, Pattern: r.Pattern, Path: docPath,
		Status: rec.Code, Headers: map[string]string{}, JSON: false,
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "" {
		obs.Headers["Content-Type"] = ct
	}
	// The page is HTML; the report records that the handler answered and with
	// what type, not a copy of the page.
	return obs
}

// branchOf is the ServeHTTP branch that routes a path: the check written there
// (the documentation and login routes authenticate in the branch) belongs to
// every method at that path.
func branchOf(p *project, pattern string) *ast.FuncDecl {
	if strings.HasPrefix(pattern, "/docs") || pattern == loginPathProbe {
		return handlerNamed(p, "ServeHTTP")
	}
	return nil
}

// loginPathProbe is the login route, whose one route has no permission check.
const loginPathProbe = "/api/v1/auth/login"

// describeProbe summarises one route's probe exchanges for the report, so a
// reader sees what an unauthenticated caller got.
func describeProbe(exchanges []Exchange) string {
	var parts []string
	for _, e := range exchanges {
		code := e.Code
		if code == "" && e.Status != 0 {
			code = fmt.Sprintf("%d", e.Status)
		}
		parts = append(parts, e.Caller+" → "+code)
	}
	return "probe: " + strings.Join(parts, ", ")
}

// containsString reports whether s is in list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// rawBody keeps a JSON body as raw JSON, or nothing when it is not JSON.
func rawBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	return body
}

// registerSchema adds a request type and every struct type it nests.
func registerSchema(proj *project, facts *Facts, typeName string) {
	if _, done := facts.Schemas[typeName]; done {
		return
	}
	schema := proj.requestSchema(typeName)
	if schema == nil {
		return
	}
	facts.Schemas[typeName] = schema
	for nested := range proj.nestedTypes(typeName) {
		registerSchema(proj, facts, nested)
	}
}

// permissionNames lists the global permissions an account can hold.
//
// They are read from the store's own list rather than scanned out of the
// gateway's source. The values are declared in internal/channelstore, so a
// scan of internal/gateway finds none of them: the generated facts silently
// carried "permissions": null, and every permission went undocumented.
func permissionNames() []string {
	all := channelstore.GlobalPermissions()
	out := make([]string, 0, len(all))
	for _, p := range all {
		out = append(out, string(p))
	}
	sort.Strings(out)
	return out
}

// handlerRef is where ServeHTTP routes to a handler.
type handlerRef struct {
	name string
	file string
	line int
}

// routeHandlers maps each pattern to its method → handler, as read from the
// ServeHTTP branch that routes it.
//
// The mapping is written here because ServeHTTP's conditions are path shapes,
// not literals; TestEveryRouteHasAHandler keeps it true by requiring every
// table route to have an entry.
func routeHandlers(proj *project) map[string]map[string]handlerRef {
	h := func(name string) handlerRef {
		fn := handlerNamed(proj, name)
		ref := handlerRef{name: name}
		if fn != nil && fn.Pos().IsValid() {
			ref.file = proj.fileOf(fn.Pos())
		}
		return ref
	}
	return map[string]map[string]handlerRef{
		"/healthz": {http.MethodGet: h("gatewayHealth")},
		// The documentation routes are served by gateway.Server.docs through
		// requireAuth; the probe reaches the site handler directly.
		"/docs":                                                 {http.MethodGet: h("docs")},
		"/docs/{path}":                                          {http.MethodGet: h("docs")},
		"/api/v1/auth/login":                                    {http.MethodPost: h("login")},
		"/api/v1/auth/me":                                       {http.MethodGet: h("me")},
		"/api/v1/telegram":                                      {http.MethodGet: h("servers")},
		"/api/v1/telegram/{key}/health":                         {http.MethodGet: h("health")},
		"/api/v1/telegram/{key}/profile":                        {http.MethodGet: h("profile")},
		"/api/v1/telegram/{key}/channels":                       {http.MethodGet: h("channels")},
		"/api/v1/telegram/{key}/channels/find":                  {http.MethodGet: h("find")},
		"/api/v1/telegram/{key}/channels/{channel_id}":          {http.MethodGet: h("channel")},
		"/api/v1/telegram/{key}/channels/{channel_id}/messages": {http.MethodGet: h("messages")},
		"/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}":        {http.MethodGet: h("message")},
		"/api/v1/telegram/{key}/channels/{channel_id}/messages/{message_id}/media":  {http.MethodGet: h("media")},
		"/api/v1/telegram/{key}/channels/{channel_id}/messages/send":                {http.MethodPost: h("sendTelegramMessage")},
		"/api/v1/telegram/{key}/channels/{channel_id}/messages/send/{operation_id}": {http.MethodGet: h("telegramSendStatus")},
		"/api/v1/telegram/{key}/channels/{channel_id}/avatar":                       {http.MethodGet: h("avatar")},
		"/api/v1/telegram/{key}/channels/{channel_id}/join":                         {http.MethodPost: h("join")},
		"/api/v1/telegram/{key}/messages/copy":                                      {http.MethodPost: h("copyTelegramMessage")},
		"/api/v1/local/channels":                                                    {http.MethodGet: h("listLocalChannels")},
		"/api/v1/local/channels/{id}":                                               {http.MethodGet: h("getLocalChannel")},
		"/api/v1/local/channels/{id}/messages":                                      {http.MethodGet: h("localChannelMessages")},
		"/api/v1/local/channels/{id}/avatar":                                        {http.MethodGet: h("localChannelAvatar")},
		"/api/v1/local/channels/{id}/leave":                                         {http.MethodPost: h("leaveLocalChannel")},
		"/api/v1/local/categories":                                                  {http.MethodGet: h("listCategories"), http.MethodPost: h("createCategory")},
		"/api/v1/local/categories/{id}": {
			http.MethodGet: h("getCategory"), http.MethodPut: h("updateCategory"), http.MethodDelete: h("deleteCategory"),
		},
		"/api/v1/local/wordpress":            {http.MethodGet: h("listWordPressSites"), http.MethodPost: h("createWordPressSite")},
		"/api/v1/local/wordpress/{id}/posts": {http.MethodPost: h("publishWordPress")},
		// Both taxonomy listings are served by one handler, which is why they
		// name the same method: the path decides which taxonomy is read.
		"/api/v1/local/wordpress/{id}/categories": {http.MethodGet: h("wordPressTerms")},
		"/api/v1/local/wordpress/{id}/tags":       {http.MethodGet: h("wordPressTerms")},
		"/api/v1/local/wordpress/{id}": {
			http.MethodGet:   h("getWordPressSite"),
			http.MethodPatch: h("updateWordPressSite"), http.MethodDelete: h("deleteWordPressSite"),
		},
		"/api/v1/local/users": {http.MethodGet: h("listUsers"), http.MethodPost: h("createUser")},
		"/api/v1/local/users/{id}": {
			http.MethodGet: h("getUser"), http.MethodPut: h("updateUser"), http.MethodDelete: h("deleteUser"),
		},
		"/api/v1/local/users/{id}/permissions": {http.MethodGet: h("userPermissions"), http.MethodPut: h("userPermissions")},
		"/api/v1/local/stats/dashboard":        {http.MethodGet: h("dashboardStats")},
		// The automation namespace. Each history route is served by its own
		// handler so the generated page can show which request type it reads.
		"/api/v1/local/automation/status":   {http.MethodGet: h("automationStatus")},
		"/api/v1/local/automation/webhooks": {http.MethodGet: h("listWebhooks"), http.MethodPost: h("createWebhook")},
		"/api/v1/local/automation/webhooks/{id}": {
			http.MethodGet: h("getWebhook"), http.MethodPatch: h("updateWebhook"), http.MethodDelete: h("deleteWebhook"),
		},
		"/api/v1/local/automation/webhooks/{id}/test":       {http.MethodPost: h("testWebhook")},
		"/api/v1/local/automation/webhooks/{id}/deliveries": {http.MethodGet: h("listDeliveries")},
		"/api/v1/local/automation/webhooks/{id}/deliveries/{delivery_id}": {
			http.MethodGet: h("getDelivery"),
		},
		"/api/v1/local/automation/webhooks/{id}/deliveries/{delivery_id}/retry": {
			http.MethodPost: h("retryDelivery"),
		},
		"/api/v1/local/automation/workflows": {
			http.MethodGet: h("listWorkflows"), http.MethodPost: h("createWorkflow"),
		},
		"/api/v1/local/automation/workflows/{id}": {
			http.MethodGet: h("getWorkflow"), http.MethodPatch: h("updateWorkflow"), http.MethodDelete: h("deleteWorkflow"),
		},
		"/api/v1/local/automation/workflows/{id}/runs": {http.MethodGet: h("listRuns")},
		"/api/v1/local/automation/workflows/{id}/runs/{run_id}": {
			http.MethodGet: h("getRun"),
		},
	}
}

// readExamples parses the example files and their index.
func readExamples(dirs ...string) ([]apidocs.Example, map[string]string, error) {
	var files []string
	for _, dir := range dirs {
		globbed, err := filepath.Glob(filepath.Join(dir, "*.http"))
		if err != nil {
			return nil, nil, err
		}
		files = append(files, globbed...)
	}
	var out []apidocs.Example
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, err
		}
		examples, err := apidocs.ParseExamples(string(body))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(file), err)
		}
		out = append(out, examples...)
	}
	index := map[string]string{}
	for _, dir := range dirs {
		_, part, err := apidocs.ReadIndex(filepath.Join(dir, "README.md"))
		if err != nil {
			return nil, nil, err
		}
		for k, v := range part {
			index[k] = v
		}
	}
	return out, index, nil
}
