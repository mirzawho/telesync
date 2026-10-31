package apidocs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Any is the path segment the probe uses where a route needs a concrete value.
// Sibling is one that is not in the table: the shape of a misspelled route.
const (
	Any     = "{any}"
	Sibling = "unknown-resource"
)

// Route is one HTTP route the public API serves.
//
// A route is a path pattern ("/api/v1/telegram/{key}/health") and the methods
// it serves. It is not a declaration: the test that uses these types verifies
// every pattern against the real handler before comparing it with the
// documentation.
type Route struct {
	Pattern string
	Methods []string
	// Probe, when set, replaces the default probe values for this route's
	// variables: the key a probe fills a placeholder with, so a route that
	// needs a known client key or document can be reached at all.
	Probe map[string]string
	// Skip omits the route from the coverage walk and the generator's
	// probe pass, with the reason. Use it only where a probe cannot reach the
	// route without consulting a live resource; Tests still require the route
	// to be documented.
	Skip string
}

// RouteAt renders one method of a route as an example file's "<path>/<method>".
func RouteAt(pattern, method string) string {
	return pattern + "/" + strings.ToLower(method)
}

// PathAt renders one route's path as an example file's "<path>/<response>".
func PathAt(pattern, response string) string {
	return pattern + "/" + response
}

// probeMethods are the methods the coverage walk tries at every probe path.
var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// telegramKey reaches the configured client the probe registers.
var telegramKey = map[string]string{"key": "official"}

// ProbePaths returns every concrete path the coverage walk visits: each route
// with Any substituted for its variables, plus the same path one segment
// deeper and one segment shallower, so a route reachable through a path the
// table does not describe is still found.
func ProbePaths() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p == "" || p == "/" || seen[p] || strings.Count(p, Any) > 3 {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, r := range Routes {
		concrete := ProbePath(r)
		add(concrete)
		// A collection route already appears one segment deeper through its
		// item route; adding "@/any" here would make e.g. /api/v1/local/users a
		// probe path and lose the rule that a 405 there means the collection
		// route was reached.
		if len(r.Methods) > 1 || strings.HasPrefix(concrete, "/api/v1/telegram/") {
			add(concrete + "/" + Any)
		}
		parts := strings.Split(concrete, "/")
		// parts[0] is empty, [1] "api", [2] "v1", [3] the namespace.
		for i := 4; i < len(parts); i++ {
			if parts[i] == "" || strings.HasPrefix(parts[i], "{") {
				continue
			}
			// The Telegram key is a configured value, not a resource name: a
			// literal there would be an unknown client and hide the resource.
			if i == 4 && parts[3] == "telegram" {
				continue
			}
			p := append(append([]string{}, parts[:i]...), Any)
			add(strings.Join(append(p, parts[i+1:]...), "/"))
			// A sibling name at this position: the shape of a typo.
			sibling := append(append([]string{}, parts[:i]...), Sibling)
			add(strings.Join(append(sibling, parts[i+1:]...), "/"))
		}
	}
	sort.Strings(out)
	return out
}

// ProbePath renders a route's path with its probe values.
func ProbePath(r Route) string {
	return variableSegment.ReplaceAllStringFunc(r.Pattern, func(m string) string {
		name := strings.Trim(m, "{}")
		if v, ok := r.Probe[name]; ok {
			return v
		}
		return Any
	})
}

// Concrete substitutes Any for every "{...}" segment of a pattern.
func Concrete(pattern string) string {
	return variableSegment.ReplaceAllString(pattern, Any)
}

var variableSegment = regexp.MustCompile(`\{[^/{}]+\}`)

// SiblingPaths returns the same paths with one literal segment replaced, one
// variant per segment. The client key is one of them; a misspelled resource is
// another, and both must be answered before any route matches.
func SiblingPaths() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range Routes {
		parts := strings.Split(Concrete(r.Pattern), "/")
		for i := range parts {
			if parts[i] == "" || strings.HasPrefix(parts[i], "{") {
				continue
			}
			p := append(append([]string{}, parts[:i]...), Any)
			p = append(p, parts[i+1:]...)
			joined := strings.Join(p, "/")
			if !seen[joined] {
				seen[joined] = true
				out = append(out, joined)
			}
		}
	}
	sort.Strings(out)
	return out
}

// --- documentation parsing ---

// DocRoutes reads every "| METHOD | `path` |" row under dir and returns the
// documented "METHOD path" pairs.
//
// The summary tables are the machine-readable index: PROSE_ROUTES covers the
// endpoints, EXAMPLES_INDEX covers the examples, and README's index links the
// two. A route may therefore be documented in two files; every other table
// that names a method and a path counts too.
//
// A row whose path names only a prefix ("/api/v1/local/...") is a namespace,
// not a route, and is skipped.
func DocRoutes(dir string) (map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "*.md"))
	if err != nil {
		return nil, err
	}
	top, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	files = append(top, files...)

	routes := map[string]bool{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "|") {
				continue
			}
			line = strings.TrimSuffix(line, "|")
			cells := strings.Split(strings.TrimPrefix(line, "|"), "|")
			if len(cells) < 2 {
				continue
			}
			method := strings.TrimSpace(cells[0])
			path := strings.Trim(strings.TrimSpace(cells[1]), "`")
			if !methodPattern.MatchString(method) || !strings.HasPrefix(path, "/") ||
				isNamespace(path) || strings.Contains(method, "/") {
				continue
			}
			routes[method+" "+normalize(path)] = true
		}
	}
	return routes, nil
}

var methodPattern = regexp.MustCompile(`^[A-Z]{3,}$`)

// --- examples ---

// Example is one documented request, executed by the test.
//
// The format is one block per request, written in examples/api.http:
//
//	METHOD /path
//	Header-Name: value          (optional, before the body)
//	Content-Type: application/json
//
//	{"json": "body"}            (optional)
//
// GET /healthz
// POST /api/v1/local/categories
// Content-Type: application/json
//
//	{"name": "Technology"}
//
// The method at RouteAt(path, method) must be listed in EXAMPLES_INDEX, so an
// example cannot exist without the index linking it, and every method the index
// lists must have an example.
type Example struct {
	Method      string
	Path        string
	Headers     []string
	ContentType string
	Body        string
}

// ParseExamples reads an example file.
func ParseExamples(body string) ([]Example, error) {
	var out []Example
	var cur *Example
	commit := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if m := requestLine.FindStringSubmatch(line); m != nil {
				commit()
				cur = &Example{Method: m[1], Path: m[2]}
				continue
			}
			if cur != nil && headerLine.MatchString(line) {
				name, value, _ := strings.Cut(line, ":")
				cur.Headers = append(cur.Headers, name)
				if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
					cur.ContentType = strings.TrimSpace(value)
				}
				continue
			}
			return nil, fmt.Errorf("line %d: %q is neither a request line nor a header", i+1, line)
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: body before any request line", i+1)
		}
		cur.Body += line + "\n"
	}
	commit()
	for i := range out {
		out[i].Body = strings.TrimSpace(out[i].Body)
	}
	return out, nil
}

var (
	requestLine = regexp.MustCompile(`^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS) (/\S*)$`)
	headerLine  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:`)
)

// variable is one "{{name}}" placeholder in an example's path or body.
var variable = regexp.MustCompile(`\{\{([a-z0-9_]+)\}\}`)

// placeholderSample is the value an id that no example captured yet is replaced
// with, so an example still validates on its own.
const placeholderSample = "1"

// captureToken prefixes the capture marker in an example path:
// "<path>/capture:<name>".
const captureToken = "capture:"

// Expand substitutes known ids into an example's path and returns the response
// token the example asks to capture, if any.
//
// The path syntax mirrors the example files: "{{name}}" is a stored id, and
// "<name" at the end of the path names the id to remember from the response's
// data.id. An unknown {{name}} becomes the sample id 1, so the example is still
// a valid request on its own.
func Expand(path string, ids map[string]string) (string, string) {
	path, query, _ := strings.Cut(path, "?")
	token := ""
	if i := strings.Index(path, "/"+captureToken); i >= 0 {
		token = strings.TrimSpace(path[i+len("/"+captureToken):])
		path = path[:i]
	}
	expanded := placeholder.ReplaceAllStringFunc(strings.TrimSpace(path), func(m string) string {
		name := strings.Trim(m, "{}")
		if id, ok := ids[name]; ok {
			return id
		}
		return placeholderSample
	})
	if query != "" {
		expanded += "?" + query
	}
	return expanded, token
}

// isNamespace reports whether a documented path names a group of routes rather
// than one ("/api/v1/local/", "/api/v1/telegram/{key}/...").
func isNamespace(path string) bool {
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, Variable)
}

// normalize turns the documentation's notations into the table's: "{{name}}" and
// "{key}" are variables like "{id}", and a trailing "/..." marks a prefix.
func normalize(path string) string {
	path = placeholder.ReplaceAllString(path, Variable)
	path = strings.TrimSuffix(path, "/...")
	path = strings.TrimSuffix(path, "/…")
	return path
}

// VariablePath renders a pattern with its variables as Variable, the form the
// documentation uses ("/api/v1/local/categories/{id}").
func VariablePath(pattern string) string {
	return strings.ReplaceAll(pattern, Any, Variable)
}

// ExtractID reads the integer id from a response: data.id, or data.<field>.id.
func ExtractID(body []byte, field string) (string, error) {
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode the response: %w", err)
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("the response has no data object")
	}
	value, ok := data[field]
	if !ok {
		return "", fmt.Errorf("the response has no data.%s", field)
	}
	if object, ok := value.(map[string]any); ok {
		value = object["id"]
	}
	number, ok := value.(json.Number)
	if !ok {
		return "", fmt.Errorf("data.%s has no numeric id", field)
	}
	if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
		return "", fmt.Errorf("data.%s has no integer id", field)
	}
	return number.String(), nil
}

// ValidateExample checks one example against the route table without running
// it: the method must be served at the path, and a body must be JSON when the
// header says so.
//
// A "{{name}}" placeholder stands for an id an earlier example captured; it is
// checked as the sample id 1, and the capturing token (a trailing "<name") is
// not part of the path.
func ValidateExample(ex Example) error {
	path, _ := Expand(ex.Path, nil)
	if !Serves(ex.Method, path) {
		return fmt.Errorf("%s %s is not a route in the table", ex.Method, path)
	}
	if ex.Body != "" {
		if ex.ContentType != "application/json" {
			return fmt.Errorf("%s %s has a body but no application/json Content-Type", ex.Method, path)
		}
		if !json.Valid([]byte(ex.Body)) {
			return fmt.Errorf("%s %s has a body that is not one JSON value", ex.Method, path)
		}
	}
	return nil
}

// Serves reports whether the table says path serves method.
//
// A pattern matches when the two have the same number of segments and every
// literal segment is equal; "{...}" segments match any non-empty value.
func Serves(method, path string) bool {
	path, _, _ = strings.Cut(path, "?")
	for _, r := range Routes {
		if !Matches(r.Pattern, path) {
			continue
		}
		for _, m := range r.Methods {
			if m == method {
				return true
			}
		}
	}
	return false
}

// Variable is the path segment the table and the documentation use for any
// concrete value.
const Variable = Any

// placeholder matches a "{{name}}" example placeholder in a documented path.
var placeholder = regexp.MustCompile(`\{\{[^}]+\}\}`)

// normalizePath turns the documentation's own notations into the table's:
// "{{name}}" is a variable, "{key}" too, and "/path/..." names a prefix.
func normalizePath(path string) string {
	path = placeholder.ReplaceAllString(path, Variable)
	path = strings.TrimSuffix(path, "/...")
	path = strings.TrimSuffix(path, "/…")
	return path
}

// Matches reports whether a concrete path fits a pattern.
func Matches(pattern, path string) bool {
	pattern, path = normalizePath(pattern), normalizePath(path)
	pp := strings.Split(strings.Trim(pattern, "/"), "/")
	cp := strings.Split(strings.Trim(path, "/"), "/")
	if len(pp) != len(cp) {
		return false
	}
	for i := range pp {
		if strings.HasPrefix(pp[i], "{") && strings.HasSuffix(pp[i], "}") {
			if cp[i] == "" {
				return false
			}
			continue
		}
		if pp[i] != cp[i] {
			return false
		}
	}
	return true
}

// ReadIndex reads the examples index table ("| Method | Route | Captured id |")
// and returns its keys ("<concrete route>/<method>"), its captured ids, and the
// example file each key came from.
func ReadIndex(path string) (map[string]bool, map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	keys := map[string]bool{}
	captures := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		method, route, capture, ok := exampleRow(line)
		if !ok {
			continue
		}
		key := RouteAt(route, strings.ToUpper(method))
		keys[key] = true
		if capture != "" {
			captures[key] = capture
		}
	}
	return keys, captures, nil
}

// exampleRow reads one examples index row.
func exampleRow(line string) (method, route, capture string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return "", "", "", false
	}
	cells := strings.Split(strings.Trim(strings.TrimPrefix(line, "|"), "|"), "|")
	if len(cells) < 2 {
		return "", "", "", false
	}
	clean := func(s string) string { return strings.Trim(strings.TrimSpace(s), "`") }
	method, route = clean(cells[0]), clean(cells[1])
	if method == "" || !strings.HasPrefix(route, "/") {
		return "", "", "", false
	}
	if len(cells) >= 3 {
		if c := clean(cells[2]); c != "" && c != "—" {
			capture = c
		}
	}
	return method, route, capture, true
}

// RouteProbeValues are the values a probe fills a variable with when the route
// names none, and are deliberately invalid: a request carrying one reaches the
// handler's own validation instead of changing state, so the probe pass can
// never write. The documentation generator uses the same table.
var RouteProbeValues = map[string]string{
	"key":        "doc-probe-client",
	"channel_id": "not-a-channel-id",
	"message_id": "not-a-message-id",
	"id":         "not-an-id",
	"path":       "missing-manual-document.md",
}
