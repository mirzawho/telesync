package apidocs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mirza/telesync/internal/apidocs"
)

// docsDir is ../.. relative to this package: the repository's docs/api.
const docsDir = "../../docs/api"

// examplesIndex is the examples page: one row per request the examples make.
const examplesIndex = "examples/README.md"

func serve(handler http.Handler, method, path, token, contentType, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// TestRouteTableMatchesGateway proves the route table against the real handler.
//
//   - Every listed method is accepted at its path (not 405, not 403: the probe
//     token holds every permission).
//   - No unlisted method is accepted: a refusal is a 405 by a route, or a
//     400/415 from a route's own validation of the empty probe request.
//   - A misspelled resource (a literal where a resource name belongs) never
//     answers 200/201/403.
//
// It cannot prove the converse for every path: a GET to a path the gateway does
// not route answers 404, and so does a GET to a real route for data the probe
// cannot see. The walk therefore reports a route the table lacks only when an
// unlisted method is accepted at it, and the index tests catch a listed route
// that vanished.
func TestRouteTableMatchesGateway(t *testing.T) {
	handler := newProbe(t)

	t.Run("every listed method is accepted", func(t *testing.T) {
		for _, r := range apidocs.Routes {
			for _, method := range r.Methods {
				path := apidocs.Concrete(r.Pattern)
				// A Telegram route is reached through a user-owned account, and
				// the operator token is a credential and not an account: it owns
				// no Telegram identity, so it is correctly refused there. The
				// probe asks those routes as the documented account holder.
				token := probeToken
				if strings.HasPrefix(path, "/api/v1/telegram/") ||
					strings.HasPrefix(path, "/api/v1/me/telegram-accounts") {
					token = handler.adminToken(t)
				}
				if method == http.MethodPut && strings.Contains(path, "/messages/") {
					token = handler.adminToken(t)
				}
				rec := serve(handler, method, path, token, "", "")
				switch rec.Code {
				case http.StatusMethodNotAllowed:
					t.Errorf("%s %s answers 405", method, path)
				case http.StatusForbidden:
					t.Errorf("%s %s answers 403: %s", method, path, strings.TrimSpace(rec.Body.String()))
				}
			}
		}
	})

	t.Run("no unlisted method is accepted", func(t *testing.T) {
		for _, p := range apidocs.ProbePaths() {
			for _, method := range probeMethods {
				rec := serve(handler, method, p, probeToken, "", "")
				if accepted(rec) && !apidocs.Serves(method, p) {
					t.Errorf("%s %s was accepted (status %d) but the table does not list it",
						method, p, rec.Code)
				}
			}
		}
	})

	t.Run("a misspelled resource is refused", func(t *testing.T) {
		for _, p := range apidocs.ProbePaths() {
			if !strings.Contains(p, apidocs.Sibling) || strings.Contains(p, apidocs.Any) {
				continue
			}
			for _, method := range probeMethods {
				// A Telegram route must be asked as somebody who owns an
				// account, or every one of them answers 403 at the account gate
				// and the walk proves nothing about the resource name. What is
				// under test is that a misspelled resource is refused *as a
				// resource* — 404, not a silent match.
				token := probeToken
				if strings.HasPrefix(p, "/api/v1/telegram/") ||
					strings.HasPrefix(p, "/api/v1/me/telegram-accounts") {
					token = handler.adminToken(t)
				}
				rec := serve(handler, method, p, token, "", "")
				switch rec.Code {
				case http.StatusOK, http.StatusCreated, http.StatusForbidden:
					t.Errorf("%s %s: status %d: %s", method, p, rec.Code,
						strings.TrimSpace(rec.Body.String()))
				}
			}
		}
	})
}

// TestRemovedRouteIsDetected deletes one route from the running table and
// expects the linked-route check to notice, then restores it.
//
// It is the test of the test: without it, a check that silently matches
// everything would keep passing while a removed endpoint went unnoticed.
func TestRemovedRouteIsDetected(t *testing.T) {
	if len(apidocs.Routes) == 0 {
		t.Fatal("the route table is empty")
	}
	saved := apidocs.Routes
	removed := saved[0]
	rest := make([]apidocs.Route, 0, len(saved)-1)
	for _, r := range saved {
		if r.Pattern != removed.Pattern {
			rest = append(rest, r)
		}
	}
	t.Cleanup(func() { apidocs.Routes = saved })
	apidocs.Routes = rest

	if matchesSomeRoute(apidocs.Concrete(removed.Pattern)) {
		t.Errorf("%s still matches a route after it was removed", apidocs.Concrete(removed.Pattern))
	}
	if apidocs.Serves(removed.Methods[0], apidocs.Concrete(removed.Pattern)) {
		t.Errorf("%s %s is still served after it was removed", removed.Methods[0], apidocs.Concrete(removed.Pattern))
	}
}

// accepted reports whether the gateway accepted a probe request rather than
// refusing its method.
//
// A 405 is always a refusal. A 400 or 415 is a route's own validation of an
// empty body or a placeholder id, which only a route performs. 401, 403, 404,
// 409 and the rest can come from a route or from routing, so this predicate
// does not call them accepted: the misspelled-resource check covers what they
// would otherwise hide.
func accepted(rec *httptest.ResponseRecorder) bool {
	switch rec.Code {
	case http.StatusBadRequest, http.StatusUnsupportedMediaType:
		return true
	}
	return false
}

// TestEveryRouteIsDocumented compares the method/path rows of the documentation
// with the table; namespaces and non-route rows are skipped.
func TestEveryRouteIsDocumented(t *testing.T) {
	documented, err := apidocs.DocRoutes(docsDir)
	if err != nil {
		t.Fatal(err)
	}

	var missing, stale []string
	for _, r := range apidocs.Routes {
		for _, method := range r.Methods {
			if !documented[method+" "+apidocs.VariablePath(r.Pattern)] {
				missing = append(missing, method+" "+apidocs.VariablePath(r.Pattern))
			}
		}
	}
	for key := range documented {
		method, path, ok := strings.Cut(key, " ")
		if !ok || !apidocs.Serves(method, path) {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, key := range missing {
		t.Errorf("%s is served but no docs/api method/path table documents it", key)
	}
	for _, key := range stale {
		t.Errorf("%s is documented but the gateway does not serve it", key)
	}
}

// TestLinkedRoutesExist checks every endpoint path the documentation writes
// outside a code example against the route table, so a reference to a renamed
// or removed endpoint fails loudly.
//
// Paths that open a code block are example requests, checked by the example
// tests instead. A path that names a group ("/api/v1/local/",
// "/api/v1/telegram/{key}/...") is not a claim that one route exists, so it is
// skipped.
func TestLinkedRoutesExist(t *testing.T) {
	pathInText := regexp.MustCompile("`(/(?:api/v1|healthz)[^`]*)`")
	err := walkMarkdown(func(rel, body string) {
		inFence := false
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			for _, m := range pathInText.FindAllStringSubmatch(line, -1) {
				target := strings.TrimRight(m[1], ".,;:")
				if isGroupPath(target) || target == "/api/v1/telegram/{key}" {
					continue
				}
				if !matchesSomeRoute(target) {
					t.Errorf("%s: %s matches no route", rel, target)
				}
			}
			// Bare paths in prose count too, but a path that is part of a URL
			// (in a JSON body, a shell command or a Markdown link) is an
			// example value and belongs to the example checks.
			for _, loc := range barePath.FindAllStringSubmatchIndex(line, -1) {
				start := loc[0]
				if start > 0 && isURLContext(line[start-1]) {
					continue
				}
				target := strings.TrimRight(line[loc[2]:loc[3]], ".,;:)")
				if isGroupPath(target) || target == "/api/v1/telegram/{key}" {
					continue
				}
				if !matchesSomeRoute(target) {
					t.Errorf("%s: %q matches no route", rel, target)
				}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

var barePath = regexp.MustCompile("(?:^|[\\s(\\[])(/(?:api/v1|healthz)[^\\s`\"',)\\[\\]]*)")

// isURLContext reports whether the byte before a path makes it part of a URL
// or a Markdown link target rather than a reference to an endpoint.
func isURLContext(b byte) bool {
	return b == '"' || b == '\'' || b == '{' || b == '(' || b == '=' || b == '/' || b == ':'
}

func isGroupPath(path string) bool {
	return strings.Contains(path, " ") || strings.HasSuffix(path, "/") ||
		strings.HasSuffix(path, "/...") || strings.Contains(path, "{{") ||
		strings.HasSuffix(path, "}") // a page's own {variable} notation
}

// matchesSomeRoute reports whether a documented path fits a table pattern. A
// query string belongs to no pattern and is ignored.
func matchesSomeRoute(path string) bool {
	path, _, _ = strings.Cut(path, "?")
	for _, r := range apidocs.Routes {
		if apidocs.Matches(r.Pattern, path) {
			return true
		}
	}
	return false
}

func walkMarkdown(fn func(rel, body string)) error { return walkMarkdownRoot(docsDir, fn) }

func walkMarkdownRoot(root string, fn func(rel, body string)) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		fn(rel, string(body))
		return nil
	})
}

// TestRelativeLinksResolve fails on a Markdown link to a file that is not
// there, or to a heading that is not there, which is what a removed or renamed
// document or section leaves behind.
//
// The pages are checked where they are published: docs/api, plus the
// hand-written sources under docs/api/manual, whose links are written
// relative to where their page lands.
func TestRelativeLinksResolve(t *testing.T) {
	inline := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	roots := []string{docsDir, filepath.Join(docsDir, "manual")}
	for _, root := range roots {
		err := walkMarkdownRoot(root, func(rel, body string) {
			for _, m := range inline.FindAllStringSubmatch(body, -1) {
				if err := checkLink(root, rel, m[1]); err != "" {
					t.Error(err)
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// checkLink reports why one link does not resolve, or "" when it does.
func checkLink(root, rel, raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "mailto:") {
		return ""
	}
	self := filepath.Join(root, rel)
	target, frag, _ := strings.Cut(raw, "#")
	if target == "" {
		if frag != "" && !hasAnchor(self, frag) {
			return rel + ": link " + strconv.Quote(raw) + " names a heading that does not exist"
		}
		return ""
	}
	// A page written for the browser may escape its target.
	if dec, err := url.PathUnescape(target); err == nil {
		target = dec
	}
	full := filepath.Join(filepath.Dir(self), target)
	if _, err := os.Stat(full); err != nil {
		return rel + ": link " + strconv.Quote(raw) + " does not resolve"
	}
	if frag != "" && strings.HasSuffix(full, ".md") && !hasAnchor(full, frag) {
		return rel + ": link " + strconv.Quote(raw) + " names a heading that does not exist"
	}
	return ""
}

// hasAnchor reports whether a document has a heading whose GitHub anchor is
// frag: lower case, punctuation dropped, spaces to hyphens.
func hasAnchor(path, frag string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if anchorOf(heading) == frag {
			return true
		}
	}
	return false
}

// anchorOf renders a heading the way GitHub does for the headings used here:
// lower case, punctuation dropped, spaces to hyphens. GitHub also removes
// underscores and keeps repeated words; this simpler form is a superset that
// still catches a renamed or removed section.
func anchorOf(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// TestExamplesAreValidAndIndexed checks each example's shape offline, then
// compares the requests it makes with the examples index: every request is
// listed, and every listed request has an example.
func TestExamplesAreValidAndIndexed(t *testing.T) {
	examples := readExamples(t)
	if len(examples) == 0 {
		t.Fatal("no examples under docs/api/examples")
	}
	indexed, captures := readIndex(t, filepath.Join(docsDir, examplesIndex))
	legacy, legacyCaptures := readIndex(t, filepath.Join(docsDir, "manual", "legacy", "examples", "README.md"))
	for k, v := range legacy {
		indexed[k] = v
	}
	for k, v := range legacyCaptures {
		captures[k] = v
	}
	if len(indexed) == 0 {
		t.Fatalf("%s lists no requests", examplesIndex)
	}

	var unlisted, unmatched []string
	for _, ex := range examples {
		key := routeKey(ex)
		if !indexed[key] {
			unlisted = append(unlisted, key)
		}
		if want, ok := captures[key]; ok {
			_, inline := apidocs.Expand(ex.Path, nil)
			if inline != want {
				t.Errorf("%s %s: index says it captures %q, the example says %q",
					ex.Method, ex.Path, want, inline)
			}
		}
	}
	for key := range indexed {
		if !exercisesSome(examples, key) {
			unmatched = append(unmatched, key)
		}
	}
	sort.Strings(unlisted)
	sort.Strings(unmatched)
	for _, key := range unlisted {
		t.Errorf("%s has an example but %s does not list it", key, examplesIndex)
	}
	for _, key := range unmatched {
		t.Errorf("%s lists %s but no example file makes it", examplesIndex, key)
	}
}

// TestIndexCoversEveryRoute requires the examples index to list every route in
// the table, so an endpoint cannot be added without an example.
func TestIndexCoversEveryRoute(t *testing.T) {
	indexed, _ := readIndex(t, filepath.Join(docsDir, examplesIndex))
	handled := handlerRoutes()
	for _, r := range apidocs.Routes {
		if r.Skip != "" || handledAmong(handled, r) {
			// A route the examples cannot express with one concrete path (one
			// entry per document) is covered by the generator instead.
			continue
		}
		for _, method := range r.Methods {
			key := apidocs.RouteAt(apidocs.Concrete(r.Pattern), method)
			if !indexed[key] {
				t.Errorf("%s does not list %s", examplesIndex, key)
			}
		}
	}
}

// TestExamplesRunAgainstTheGateway executes every example against the probe
// handler with its real method, path, headers and body.
//
// The examples share one handler, so create → read → update → delete runs in
// order. The id a create assigns is captured and substituted into the later
// examples, exactly as a person following the page would.
func TestExamplesRunAgainstTheGateway(t *testing.T) {
	handler := newProbe(t)
	_, captures := readIndex(t, filepath.Join(docsDir, examplesIndex))
	examples := readExamples(t)

	ids := map[string]string{}
	ran := 0
	for _, ex := range examples {
		path, inline := apidocs.Expand(ex.Path, ids)
		body := ex.Body
		for name, id := range ids {
			body = strings.ReplaceAll(body, "{{"+name+"}}", id)
		}
		capture := captures[routeKey(ex)]
		if inline != "" && capture != inline {
			t.Errorf("%s %s captures %q inline but the index says %q", ex.Method, ex.Path, inline, capture)
			continue
		}
		if capture != "" && inline == "" {
			t.Errorf("%s %s is indexed as capturing %q but does not say so", ex.Method, ex.Path, capture)
			continue
		}
		token := probeToken
		if strings.Contains(path, "/edit/") ||
			strings.HasPrefix(path, "/api/v1/me/telegram-accounts") ||
			strings.HasPrefix(path, "/api/v1/local/") ||
			(ex.Method == http.MethodPut && strings.Contains(path, "/messages/")) {
			token = handler.adminToken(t)
		}
		rec := serve(handler, ex.Method, path, token, ex.ContentType, body)
		if strings.HasPrefix(path, "/api/v1/me/telegram-accounts") && rec.Code != http.StatusOK {
			t.Errorf("%s %s answered %d: %s", ex.Method, path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		switch rec.Code {
		case http.StatusUnauthorized:
			// The probe can authenticate; a 401 here means the example
			// reached the route but its path, method or credential is wrong.
			t.Errorf("%s %s answered 401: %s", ex.Method, path, strings.TrimSpace(rec.Body.String()))
		case http.StatusNotFound:
			t.Errorf("%s %s answered 404: %s", ex.Method, path, strings.TrimSpace(rec.Body.String()))
		}
		if capture != "" {
			field := capture
			if field == "root" {
				field = ""
			}
			id, err := apidocs.ExtractID(rec.Body.Bytes(), field)
			if err != nil {
				t.Errorf("%s %s: %v: %s", ex.Method, path, err, strings.TrimSpace(rec.Body.String()))
			} else {
				ids[capture] = id
			}
		}
		ran++
	}
	if ran == 0 {
		t.Fatal("no example was executed")
	}
	for name, id := range ids {
		if id == "" || id == "0" {
			t.Errorf("captured id %q is %q", name, id)
		}
	}
}

// handledAmong reports whether a route is one the documentation handler serves
// for every path at once, so it is not listed per document.
func handledAmong(handled map[string]bool, r apidocs.Route) bool {
	for _, m := range r.Methods {
		if handled[m+" "+r.Pattern] {
			return true
		}
	}
	return false
}

// exampleFiles lists every example file: the shared one under docs/api/examples
// and the legacy copy the pages used before the generated documents existed.
func exampleFiles() ([]string, error) {
	var files []string
	for _, dir := range []string{filepath.Join(docsDir, "examples"), filepath.Join(docsDir, "manual", "legacy", "examples")} {
		globbed, err := filepath.Glob(filepath.Join(dir, "*.http"))
		if err != nil {
			return nil, err
		}
		files = append(files, globbed...)
	}
	return files, nil
}

// readExamples parses and validates every example file.
// handlerRoutes are the method/path pairs the documentation handler serves:
// GET for every document, not a table entry per document.
func handlerRoutes() map[string]bool {
	out := map[string]bool{}
	// The documentation is a page shell, not a JSON route: like /docs, its GET
	// is a document and needs no .http example of its own.
	for _, pattern := range []string{"/docs", "/docs/{path}"} {
		for _, r := range apidocs.Routes {
			if r.Pattern == pattern {
				for _, m := range r.Methods {
					out[m+" "+pattern] = true
				}
			}
		}
	}
	return out
}

func readExamples(t *testing.T) []apidocs.Example {
	t.Helper()
	files, err := exampleFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no example files under docs/api/examples")
	}
	var out []apidocs.Example
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		examples, err := apidocs.ParseExamples(string(body))
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(file), err)
		}
		if len(examples) == 0 {
			t.Errorf("%s: no requests", filepath.Base(file))
		}
		for _, ex := range examples {
			if err := apidocs.ValidateExample(ex); err != nil {
				t.Errorf("%s: %v", filepath.Base(file), err)
			}
			out = append(out, ex)
		}
	}
	return out
}

// routeKey is an example's index key: the table pattern it fits, spaces and
// query string removed, and the method.
func routeKey(ex apidocs.Example) string {
	path, _ := apidocs.Expand(ex.Path, nil)
	path, _, _ = strings.Cut(path, "?")
	return indexKey(path, ex.Method)
}

// indexKey is the table route a concrete or documented path belongs to, with
// its variables written as {any}.
func indexKey(path, method string) string {
	for _, r := range apidocs.Routes {
		if apidocs.Matches(r.Pattern, path) {
			return apidocs.RouteAt(apidocs.Concrete(r.Pattern), method)
		}
	}
	return apidocs.RouteAt(path, method)
}

// exercisesSome reports whether any example produces an index key.
func exercisesSome(examples []apidocs.Example, key string) bool {
	for _, ex := range examples {
		if routeKey(ex) == key {
			return true
		}
	}
	return false
}

// readIndex reads the examples index table: "| Method | Route | Captured id |".
// The second return value is the routes whose row names a captured id.
func readIndex(t *testing.T, path string) (map[string]bool, map[string]string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	keys := map[string]bool{}
	captures := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimPrefix(line, "|"), "|"), "|")
		if len(cells) < 2 {
			continue
		}
		method := strings.Trim(strings.TrimSpace(cells[0]), "`")
		route := strings.Trim(strings.TrimSpace(cells[1]), "`")
		if method == "" || !strings.HasPrefix(route, "/") {
			continue
		}
		// The index writes "{{name}}" where an example captured an id; DocRoutes
		// normalises both sides to {any}, so the index is normalised here too.
		// "{key}" and "{id}" too: an index row names a parameter either way.
		route = regexp.MustCompile(`\{\{[a-z0-9_]+\}\}|\{[a-z_0-9]+\}`).ReplaceAllString(route, apidocs.Any)
		key := apidocs.RouteAt(route, strings.ToUpper(method))
		keys[key] = true
		if len(cells) >= 3 {
			capture := strings.Trim(strings.TrimSpace(cells[2]), "`")
			if capture != "" && capture != "—" {
				captures[key] = capture
			}
		}
	}
	return keys, captures
}
