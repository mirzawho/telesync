package docsgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mirza/telesync/internal/apidocs"
)

// Caller is one caller the generator replays a route with.
type Caller struct {
	// ID names the caller in the output ("anonymous", "operator", "user 3").
	ID string
	// Cred is the frozen credential the probe sends.
	Cred apidocs.Credential
	// Anonymous is true when no credential is sent.
	Anonymous bool
}

// Callers returns the callers in the order the generator tries them: no
// credential first, then the operator token, then accounts from the least to
// the most privileged.
func Callers(p apidocs.Prober) []Caller {
	out := []Caller{{ID: "anonymous", Cred: p.Anonymous(), Anonymous: true}}
	if c, ok := p.Operator(); ok {
		out = append(out, Caller{ID: "operator", Cred: c})
	}
	out = append(out,
		Caller{ID: "limited user", Cred: p.Limited(func(admin apidocs.Credential) string {
			return permissionsBody(admin, nil)
		})},
		Caller{ID: "administrator", Cred: p.Administrator()},
	)
	return out
}

// permissionsBody is a permission set for p.Limited. It grants one category and
// no channels, so the limited caller can read and update that category but has
// no channel access and no global permission.
func permissionsBody(_ apidocs.Credential, categoryID *string) string {
	category := "1"
	if categoryID != nil {
		category = *categoryID
	}
	body := map[string]any{
		"global_permissions":  []string{},
		"channel_permissions": []any{},
		"category_permissions": []any{map[string]any{
			"category_id": json.Number(category),
			"permissions": map[string]string{"view": "allow", "update": "allow", "delete": "allow"},
		}},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return `{}`
	}
	return string(raw)
}

// Observation is one (route, method, caller) exchange.
type Observation struct {
	Method  string
	Pattern string
	// Path is what was sent, after filling in probe values.
	Path       string
	Caller     string
	Status     int
	Code       string
	JSON       bool
	Headers    map[string]string
	Body       []byte
	Request    string
	Fresh      bool // the credential was unchanged after the request
	Unresolved string
}

// ObserveProbe is the probe mode: it replays every route with every caller.
//
// Each route is sent with a deliberately invalid probe value where it has a
// variable ("not-a-valid-value"), so a handler's validation answers 400 and no
// route in this pass can change state. A 200 can therefore only come from a
// route that accepts anything, and routes that do write are covered by the
// example snapshots instead.
func ObserveProbe(ctx context.Context, p apidocs.Prober, routes []apidocs.Route, callers []Caller) []Observation {
	var out []Observation
	for _, r := range routes {
		if r.Skip != "" {
			continue
		}
		for _, method := range r.Methods {
			for _, c := range callers {
				obs := exchange(ctx, p, r, method, c, "")
				out = append(out, obs)
			}
		}
	}
	return out
}

// exchange sends one request.
func exchange(ctx context.Context, p apidocs.Prober, r apidocs.Route, method string, c Caller, body string) Observation {
	path := fillPattern(r)
	rec := p.Do(ctx, method, path, c.Cred.Authorization(), body)
	obs := Observation{
		Method: method, Pattern: r.Pattern, Path: path, Caller: c.ID,
		Status: rec.Code, Request: c.Cred.Request(),
		Headers: map[string]string{},
		Fresh:   c.Cred.Snapshot(ctx),
	}
	if looksLikeJSON(rec.Body.Bytes()) {
		obs.JSON = true
		obs.Body = rec.Body.Bytes()
		var env struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(obs.Body, &env); err == nil && env.Error != nil {
			obs.Code = env.Error.Code
		}
	}
	for _, h := range []string{"Allow", "WWW-Authenticate", "Retry-After", "ETag", "Cache-Control", "Warning", "Content-Type"} {
		if v := rec.Header().Get(h); v != "" {
			obs.Headers[h] = redactedHeader(h, v)
		}
	}
	return obs
}

// looksLikeJSON reports whether a probe response is a JSON document.
//
// The test is on the body, not the Content-Type header: a response that
// declared an error status before writing JSON keeps the bytes it wrote while
// the header it declared may not match, so the bytes are the evidence.
func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') &&
		json.Valid(trimmed) || (len(trimmed) > 0 && json.Valid(bytes.TrimRight(trimmed, "\n")))
}

// probeValues are the placeholders a pattern's variables are filled with in
// probe mode. All of them are invalid on purpose.
var probeValues = map[string]string{
	"key":        "doc-probe-client",
	"channel_id": "not-a-channel-id",
	"message_id": "not-a-message-id",
	"id":         "not-an-id",
	"path":       "missing-manual-document.md",
}

// fillPattern replaces every variable with its probe value: the route's own
// value first, then the package default.
func fillPattern(r apidocs.Route) string {
	return variableInPattern.ReplaceAllStringFunc(r.Pattern, func(m string) string {
		name := strings.Trim(m, "{}")
		if v, ok := r.Probe[name]; ok {
			return v
		}
		if v, ok := apidocs.RouteProbeValues[name]; ok {
			return v
		}
		return "invalid"
	})
}

var variableInPattern = regexp.MustCompile(`\{[^/{}]+\}`)

// redactedHeader keeps a header's shape but removes values that vary per
// request (nothing here does, but ETags embed photo ids that a live run would).
func redactedHeader(name, value string) string {
	switch name {
	case "ETag":
		return `"<value>"`
	}
	return value
}

// --- source-derived rules ---

// project is the parsed slice of the gateway needed for the rules.
type project struct {
	fset   *token.FileSet
	files  []*ast.File
	funcs  map[string]*ast.FuncDecl
	types  map[string]*ast.TypeSpec
	consts map[string]string
	// methods maps a receiver type name to its methods by name.
	methods map[string]map[string]*ast.FuncDecl
	// bodies holds every function body by name, because FuncDecl.Body is
	// empty for declarations parsed by parser.ParseDir.
	bodies map[string]*ast.BlockStmt
	// followLiterals, while set, walks function literals too. It is set only
	// while scanning a ServeHTTP branch, where the literal is the route.
	followLiterals bool
}

// loadProject parses the gateway package compiled into this test binary.
func loadProject(dir string) (*project, error) {
	p := &project{
		fset:    token.NewFileSet(),
		funcs:   map[string]*ast.FuncDecl{},
		types:   map[string]*ast.TypeSpec{},
		consts:  map[string]string{},
		methods: map[string]map[string]*ast.FuncDecl{},
		bodies:  map[string]*ast.BlockStmt{},
	}
	// SkipObjectResolution: the bodies are what the rules are read from, and
	// the resolver is not needed for them.
	pkgs, err := parser.ParseDir(p.fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			p.files = append(p.files, f)
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Body != nil {
						p.bodies[d.Name.Name] = d.Body
					}
					if d.Recv != nil && len(d.Recv.List) == 1 {
						recv := receiverType(d.Recv.List[0].Type)
						if p.methods[recv] == nil {
							p.methods[recv] = map[string]*ast.FuncDecl{}
						}
						p.methods[recv][d.Name.Name] = d
						continue
					}
					if d.Recv == nil {
						p.funcs[d.Name.Name] = d
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.TypeSpec:
							p.types[s.Name.Name] = s
						case *ast.ValueSpec:
							if d.Tok == token.CONST {
								p.consts[s.Names[0].Name] = constValue(s)
							}
						}
					}
				}
			}
		}
	}
	return p, nil
}

func receiverType(e ast.Expr) string {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func constValue(s *ast.ValueSpec) string {
	if len(s.Values) == 0 {
		return ""
	}
	if lit, ok := s.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		v, err := strconv.Unquote(lit.Value)
		if err == nil {
			return v
		}
	}
	return ""
}

// printNode renders a node back to Go source, one line, for evidence.
func (p *project) printNode(n ast.Node) string {
	if n == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, p.fset, n); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

// body returns a function's body, from the declaration or the parsed file.
func (p *project) body(fn *ast.FuncDecl) *ast.BlockStmt {
	if fn == nil {
		return nil
	}
	if fn.Body != nil {
		return fn.Body
	}
	return p.bodies[fn.Name.Name]
}

// fileOf returns the base name of the file a node lives in.
func (p *project) fileOf(pos token.Pos) string {
	if !pos.IsValid() {
		return ""
	}
	return path.Base(p.fset.Position(pos).Filename)
}

// credentialCheck is the permission call a method makes on the caller.
type credentialCheck struct {
	// Expression is the call as written, e.g. `actorAuth(r).Global(...)`.
	Expression string
	// Permission is the permission it names (a constant's value), if one.
	Permission string
	// Kind and Action are set for a resource check (`.Allowed(kind, action, …)`).
	Kind   string
	Action string
	// In is the file and function the check is written in.
	In string
}

// routeBranches reads ServeHTTP's top-level ifs and, for each, the route
// prefix its condition names.
//
// A branch is recognised by the shape of its condition, not its position: the
// condition compares r.URL.Path and mentions a string (a literal or a named
// constant whose value is recovered). The branches are returned in source
// order, so a route's conditions are the ones in its own branch.
type routeBranch struct {
	prefix string
	ift    *ast.IfStmt
}

// branches reads ServeHTTP's branch list.
func (p *project) branches() []routeBranch {
	serve := handlerNamed(p, "ServeHTTP")
	body := p.body(serve)
	if body == nil {
		return nil
	}
	var out []routeBranch
	for _, stmt := range body.List {
		ifStmt, ok := stmt.(*ast.IfStmt)
		if !ok || !strings.Contains(p.printNode(ifStmt.Cond), "URL.Path") {
			continue
		}
		out = append(out, routeBranch{prefix: p.pathNamed(ifStmt.Cond), ift: ifStmt})
	}
	return out
}

// pathNamed is the path a branch condition names: the first string literal or
// constant the condition mentions. A short string ("/", "") is not a path: it
// is a comparison against a substring (docsPath+"/"), and the branch is found
// through its helper call instead.
func (p *project) pathNamed(cond ast.Expr) string {
	named := ""
	ast.Walk(visitor(func(n ast.Node) (bool, bool) {
		if named != "" {
			return false, true
		}
		switch node := n.(type) {
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				named, _ = strconv.Unquote(node.Value)
			}
		case *ast.SelectorExpr:
			// A constant of the gateway package, qualified or not.
			if v, ok := p.consts[node.Sel.Name]; ok && strings.HasPrefix(v, "/") && len(v) > 1 {
				named = v
			}
		case *ast.Ident:
			if v, ok := p.consts[node.Name]; ok && strings.HasPrefix(v, "/") && len(v) > 1 {
				named = v
			}
		}
		return false, false
	}), cond)
	if len(named) <= 1 {
		return ""
	}
	return named
}

// helperNamed is a function or constant the branch condition calls and that
// names a route (pathPrefix, statsAccounts, …).
func (p *project) helperNamed(cond ast.Expr) string {
	name := ""
	ast.Walk(visitor(func(n ast.Node) (bool, bool) {
		if name != "" {
			return false, true
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && handlerNamed(p, id.Name) != nil {
				name = id.Name
			}
		}
		return false, false
	}), cond)
	return name
}

// branchFor is the branch a route's path belongs to: the branch whose prefix
// is the longest one the route starts with.
func (p *project) branchFor(pattern string) *ast.IfStmt {
	best := ""
	var found *ast.IfStmt
	for _, b := range p.branches() {
		if b.prefix == "" {
			continue
		}
		if pattern == b.prefix || strings.HasPrefix(pattern, strings.TrimSuffix(b.prefix, "/")+"/") {
			if len(b.prefix) > len(best) {
				best, found = b.prefix, b.ift
			}
		}
	}
	if found != nil {
		return found
	}
	// A branch may dispatch through a helper called on the request path (a
	// prefix test in a function). Such a branch names no string itself.
	for _, b := range p.branches() {
		if b.prefix != "" {
			continue
		}
		if p.branchNames(b.ift, pattern) {
			return b.ift
		}
	}
	return nil
}

// branchNames reports whether a branch's body reaches a helper that names the
// route.
func (p *project) branchNames(ift *ast.IfStmt, pattern string) bool {
	reached := false
	ast.Walk(visitor(func(n ast.Node) (bool, bool) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return false, false
		}
		name := ""
		switch f := call.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		fn := handlerNamed(p, name)
		if fn == nil {
			return false, false
		}
		body := p.printNode(p.body(fn))
		if strings.Contains(body, pattern) || strings.Contains(body, strings.TrimSuffix(pattern, "/{path}")) {
			reached = true
			return false, true
		}
		return false, false
	}), ift.Body)
	return reached
}

// credentialsOfRoute is the route's conditions: those made on the way to the
// handler. The gateway resolves the caller once, before its branch dispatch, so
// every route inherits the conditions of the ServeHTTP statements before its
// branch; the branch's own statements (and a wrapper it wraps the handler in)
// and the handler's call chain add to them.
//
// Conditions are collected in need order, not by walking the whole function:
// every top-level statement before the branch, then the branch, then the
// handler. A route that shares a helper with another still gets its own list.
func (p *project) credentialsOfRoute(pattern string, handler *ast.FuncDecl) []credentialCheck {
	seen := map[string]bool{}
	recorded := map[string]bool{}
	var out []credentialCheck
	add := func(owner string, body *ast.BlockStmt, followLiterals bool) {
		save := p.followLiterals
		p.followLiterals = followLiterals
		p.collectCredentials(owner, body, seen, recorded, &out, 0)
		p.followLiterals = save
	}
	if branch := p.branchFor(pattern); branch != nil {
		// The statements before the branch apply to every route that reaches
		// it: the caller is resolved there.
		for _, stmt := range p.body(handlerNamed(p, "ServeHTTP")).List {
			if stmt == branch {
				break
			}
			if block := statementBlock(stmt); block != nil {
				add("ServeHTTP", block, false)
			}
		}
		add("ServeHTTP", branch.Body, true)
	}
	if owner := handler.Name.Name; !seen[owner] {
		seen[owner] = true
		add(owner, p.body(handler), false)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Expression < out[j].Expression })
	return out
}

// statementBlock is the body of the statements whose conditions are code that
// runs before a branch: an assignment with a call, a block, or a short
// variable declaration with a call.
func statementBlock(stmt ast.Stmt) *ast.BlockStmt {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		return &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: callOf(s.Rhs)}}}
	case *ast.BlockStmt:
		return s
	}
	return nil
}

// callOf is the first call expression in an expression list.
func callOf(exprs []ast.Expr) ast.Expr {
	for _, e := range exprs {
		if _, ok := e.(*ast.CallExpr); ok {
			return e
		}
	}
	return &ast.Ident{Name: "_"}
}

// credentialsOf finds the authorization conditions a handler makes.
//
// It reads the handler's own body and, transitively, the package functions and
// Server methods it calls or passes as a handler, so a rule behind a helper is
// found where it is applied. It cannot follow interface dispatch, reflection or
// a function value stored in a variable; what it cannot see is reported.
func (p *project) credentialsOf(handler *ast.FuncDecl) []credentialCheck {
	return p.credentialsOfTwo(handler, nil)
}

// credentialsOfTwo scans two bodies as one route: ServeHTTP's own branch and
// the handler it routes to both apply to the same request.
func (p *project) credentialsOfTwo(first, second *ast.FuncDecl) []credentialCheck {
	seen := map[string]bool{}
	recorded := map[string]bool{}
	var out []credentialCheck
	for _, fn := range []*ast.FuncDecl{first, second} {
		if fn == nil {
			continue
		}
		seen[fn.Name.Name] = true
	}
	// The branch is scanned first: its checks run before the handler's, and
	// the order is part of what the page shows.
	for _, fn := range []*ast.FuncDecl{first, second} {
		if fn == nil {
			continue
		}
		p.collectCredentials(fn.Name.Name, p.body(fn), seen, recorded, &out, 0)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Expression < out[j].Expression })
	return out
}

// collectCredentials records the authorization conditions in one body and,
// transitively, in the package functions and methods it calls.
func (p *project) collectCredentials(owner string, body *ast.BlockStmt, seen, recorded map[string]bool, out *[]credentialCheck, depth int) {
	if body == nil || depth > 4 {
		return
	}
	ast.Walk(visitor(func(n ast.Node) (skipChildren, stop bool) {
		switch node := n.(type) {
		// A nested function is its own scope: its calls are not this
		// function's conditions, and following them would attribute another
		// route's rules to this one.
		case *ast.FuncLit:
			// A nested function is its own scope: its calls are not this
			// function's conditions, and following them would attribute
			// another route's rules to this one. A branch that wraps a
			// handler is the exception: there the literal is the branch.
			if p.followLiterals {
				p.collectCredentials(owner, node.Body, seen, recorded, out, depth+1)
			}
			return true, false
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Global", "Allowed", "CanList", "HasAny", "requireAuth", "authenticated", "authenticate":
					key := p.printNode(node)
					if !recorded[key] {
						recorded[key] = true
						check := p.credentialCall(node, sel)
						check.In = p.fileOf(body.Pos()) + ":" + owner
						*out = append(*out, check)
					}
				}
			}
			// A call to a package function or a Server method, or such a
			// function passed as an argument (http.HandlerFunc(next)), applies
			// that function's rule: its body is where the check is written.
			var name string
			switch f := node.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			p.follow(name, seen, recorded, out, depth)
			for _, arg := range node.Args {
				if id, ok := arg.(*ast.Ident); ok {
					p.follow(id.Name, seen, recorded, out, depth)
				}
			}
		}
		return false, false
	}), body)
}

// follow walks a function's body when it carries a rule. A name is looked up
// on Server first, then in the package.
func (p *project) follow(name string, seen, recorded map[string]bool, out *[]credentialCheck, depth int) {
	if name == "" || seen[name] {
		return
	}
	fn := handlerNamed(p, name)
	if fn == nil {
		return
	}
	seen[name] = true
	p.collectCredentials(name, p.body(fn), seen, recorded, out, depth+1)
}

// visitor adapts a function to ast.Visitor. The first return value tells
// ast.Walk whether to skip the node's children.
type visitor func(ast.Node) (skipChildren, stop bool)

func (v visitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}
	skip, stop := v(n)
	if skip || stop {
		return nil
	}
	return v
}

func (p *project) credentialCall(call *ast.CallExpr, sel *ast.SelectorExpr) credentialCheck {
	check := credentialCheck{Expression: p.printNode(call)}
	switch sel.Sel.Name {
	case "Global", "CanList":
		if len(call.Args) > 0 {
			check.Permission, check.Kind = p.argumentName(call.Args[0])
		}
	case "HasAny":
		if len(call.Args) > 0 {
			_, check.Kind = p.argumentName(call.Args[0])
		}
	case "Allowed":
		if len(call.Args) >= 2 {
			_, check.Kind = p.argumentName(call.Args[0])
			check.Action, _ = p.argumentName(call.Args[1])
		}
	}
	return check
}

// argumentName resolves an argument that names a package constant.
func (p *project) argumentName(e ast.Expr) (value, name string) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	if v, ok := p.consts[sel.Sel.Name]; ok {
		return v, sel.Sel.Name
	}
	return sel.Sel.Name, sel.Sel.Name
}

// requestTypes walks from a handler to the types it decodes JSON bodies into.
//
// Two shapes are recognised, both of which the gateway writes:
//
//	var req T; decodeJSONBody(w, r, &req)
//	decodeCategoryRequest(w, r) — the helper decodes into its own T
//
// The route's branch is scanned too: the login route's handler is reached
// through a wrapper, so its body is the only place the decode is visible.
func (p *project) requestTypes(handlers ...*ast.FuncDecl) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, fn := range handlers {
		if fn == nil {
			continue
		}
		add(p.decodeTarget(fn))
		// A helper the body calls: its own body holds the decode.
		ast.Inspect(p.body(fn), func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch f := call.Fun.(type) {
			case *ast.Ident:
				name = f.Name
			case *ast.SelectorExpr:
				name = f.Sel.Name
			}
			if strings.HasPrefix(name, "decode") {
				add(p.decodeTarget(handlerNamed(p, name)))
			}
			return true
		})
	}
	return out
}

// decodeTarget returns the type a function decodes into, from a decode call
// whose argument is &local or &T{}.
func (p *project) decodeTarget(fn *ast.FuncDecl) string {
	if fn == nil || p.body(fn) == nil {
		return ""
	}
	locals := map[string]string{}
	ast.Inspect(p.body(fn), func(n ast.Node) bool {
		decl, ok := n.(*ast.DeclStmt)
		if !ok {
			return true
		}
		gd, ok := decl.Decl.(*ast.GenDecl)
		if !ok {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || len(vs.Values) != 0 {
				continue
			}
			if t, ok := vs.Type.(*ast.Ident); ok {
				locals[vs.Names[0].Name] = t.Name
			}
		}
		return true
	})
	target := ""
	ast.Inspect(p.body(fn), func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || target != "" {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || !strings.HasPrefix(id.Name, "decode") {
			return true
		}
		for _, arg := range call.Args {
			unary, ok := arg.(*ast.UnaryExpr)
			if !ok {
				continue
			}
			switch x := unary.X.(type) {
			case *ast.CompositeLit:
				if t, ok := x.Type.(*ast.Ident); ok {
					target = t.Name
				}
			case *ast.Ident:
				if t, ok := locals[x.Name]; ok {
					target = t
				}
			}
		}
		return true
	})
	return target
}

// requestSchema renders a request type as a JSON schema document, keeping the
// Go types next to the JSON types so a reader sees both.
func (p *project) requestSchema(typeName string) map[string]any {
	spec, ok := p.types[typeName]
	if !ok {
		return nil
	}
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		return nil
	}
	properties := map[string]any{}
	var required []string
	var order []string
	for _, field := range st.Fields.List {
		name, opts := p.fieldName(field)
		if name == "" {
			continue
		}
		prop := map[string]any{"go_type": p.printNode(field.Type)}
		if desc := p.commentOf(field); desc != "" {
			prop["description"] = desc
		}
		switch {
		case strings.HasPrefix(opts, "pointer"):
			prop["nullable"] = true
		case strings.HasPrefix(opts, "slice"):
			prop["items"] = p.printNode(field.Type.(*ast.ArrayType).Elt)
		}
		properties[name] = prop
		order = append(order, name)
		if jsonType, ok := jsonTypeOf(field.Type); ok {
			prop["type"] = jsonType
		}
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
		if ref, ok := objectRef(field.Type); ok {
			prop["$ref"] = "#/components/schemas/" + ref
		}
	}
	sort.Strings(required)
	return map[string]any{
		"go_type":    typeName,
		"properties": ordered(properties, order),
		"required":   required,
	}
}

// nestedTypes lists the struct types a request type contains, directly or in a
// slice, so their schemas are written too.
func (p *project) nestedTypes(typeName string) map[string]bool {
	out := map[string]bool{}
	spec, ok := p.types[typeName]
	if !ok {
		return out
	}
	st, ok := spec.Type.(*ast.StructType)
	if !ok {
		return out
	}
	for _, field := range st.Fields.List {
		t := field.Type
		if arr, ok := t.(*ast.ArrayType); ok {
			t = arr.Elt
		}
		id, ok := t.(*ast.Ident)
		if !ok {
			continue
		}
		if spec, ok := p.types[id.Name]; ok {
			if _, isStruct := spec.Type.(*ast.StructType); isStruct {
				out[id.Name] = true
			}
		}
	}
	return out
}

// fieldName reads a field's JSON name and the options it was tagged with.
func (p *project) fieldName(field *ast.Field) (string, string) {
	if field.Tag == nil {
		return "", ""
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", ""
	}
	tag := structTag(raw)
	name, opts, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", ""
	}
	if name == "" && len(field.Names) > 0 {
		name = field.Names[0].Name
	}
	var kind string
	switch field.Type.(type) {
	case *ast.StarExpr:
		kind = "pointer"
	case *ast.ArrayType:
		kind = "slice"
	}
	if kind != "" {
		opts = kind + " " + opts
	}
	return name, opts
}

func structTag(raw string) string {
	const key = `json:"`
	i := strings.Index(raw, key)
	if i < 0 {
		return ""
	}
	rest := raw[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func (p *project) commentOf(n ast.Node) string {
	var group *ast.CommentGroup
	switch v := n.(type) {
	case *ast.Field:
		group = v.Doc
	case *ast.TypeSpec:
		group = v.Doc
	case *ast.FuncDecl:
		group = v.Doc
	}
	if group == nil {
		return ""
	}
	return strings.Join(strings.Fields(group.Text()), " ")
}

// jsonTypeOf maps the Go types the request bodies use.
func jsonTypeOf(e ast.Expr) (string, bool) {
	switch t := e.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			return "string", true
		case "bool":
			return "boolean", true
		case "int", "int64", "int32":
			return "integer", true
		}
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			switch id.Name {
			case "string":
				return "string", true
			case "bool":
				return "boolean", true
			case "int", "int64":
				return "integer", true
			}
		}
	case *ast.ArrayType:
		return "array", true
	}
	return "", false
}

// objectRef returns the struct type a field nests, for the referenced schema.
func objectRef(e ast.Expr) (string, bool) {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.ArrayType:
		if id, ok := t.Elt.(*ast.Ident); ok {
			return id.Name, true
		}
	case *ast.MapType:
		return "", false
	}
	return "", false
}

// ordered keeps the properties in source order in the JSON output.
func ordered(props map[string]any, order []string) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, name := range order {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		buf.Write(key)
		buf.WriteByte(':')
		value, _ := json.MarshalIndent(props[name], "", "  ")
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// responseOf names the value a handler writes as its success document, and the
// status it writes it with, by reading the handler's own writeJSON calls.
//
// writeJSON(w, status, value): the status is the second argument and the value
// the third. A handler with no such call writes something else (bytes, a
// template) and is reported as such rather than guessed at.
func (p *project) responseOf(handler *ast.FuncDecl) (value, status string) {
	ast.Inspect(p.body(handler), func(n ast.Node) bool {
		if value != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		// writeJSON is called both ways in the gateway: as s.writeJSON and,
		// from the package functions, as writeJSON.
		name := ""
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		if name != "writeJSON" || len(call.Args) != 3 {
			return true
		}
		value = p.printNode(call.Args[2])
		status = p.statusName(call.Args[1])
		return true
	})
	return value, status
}

// statusName resolves a writeJSON status argument to its http package name.
func (p *project) statusName(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" {
		if _, known := httpStatusText[sel.Sel.Name]; known {
			return sel.Sel.Name
		}
	}
	return ""
}

// methodsOfHandler resolves the method name a ServeHTTP branch calls
// ("s.channels", "s.health", …) for one resource and segment count.
func handlerNamed(p *project, name string) *ast.FuncDecl {
	if m := p.methods["Server"]; m != nil {
		if fn := m[name]; fn != nil {
			return fn
		}
	}
	return p.funcs[name]
}

// httpStatusText maps the http.Status* names the gateway uses to their text.
var httpStatusText = map[string]string{
	"StatusOK":                   "200 OK",
	"StatusCreated":              "201 Created",
	"StatusBadRequest":           "400 Bad Request",
	"StatusUnauthorized":         "401 Unauthorized",
	"StatusForbidden":            "403 Forbidden",
	"StatusNotFound":             "404 Not Found",
	"StatusMethodNotAllowed":     "405 Method Not Allowed",
	"StatusConflict":             "409 Conflict",
	"StatusUnsupportedMediaType": "415 Unsupported Media Type",
	"StatusTooManyRequests":      "429 Too Many Requests",
	"StatusInternalServerError":  "500 Internal Server Error",
	"StatusBadGateway":           "502 Bad Gateway",
	"StatusServiceUnavailable":   "503 Service Unavailable",
	"StatusGatewayTimeout":       "504 Gateway Timeout",
}

// httpStatus renders "StatusOK" as 200.
func httpStatus(name string) int {
	text, ok := httpStatusText[name]
	if !ok {
		return 0
	}
	return httpCode(text)
}

// httpCode reads the number from "200 OK".
func httpCode(text string) int {
	code, err := strconv.Atoi(strings.SplitN(text, " ", 2)[0])
	if err != nil {
		return 0
	}
	return code
}

// --- snapshot mode ---

// Snapshot is what the examples proved about a route.
type Snapshot struct {
	Method  string
	Pattern string
	Path    string
	Caller  string
	Status  int
	Code    string
	JSON    bool
	Body    []byte
	Request string
}

// Store is the documented resource ids the permission example needs.
type Store struct {
	CategoryID string
}

// ExampleCallers are the callers the examples run as.
type ExampleCallers struct {
	Operator apidocs.Credential
}

// ObservesExamples replays every request in the example file and records the
// exchanges that carry data: every non-error answer, plus the documented
// refusals (400, 401, 403, 409, 415) that state a rule. It runs on a fresh
// probe and, importantly, before anything else uses the probe's database, so
// the ids the examples create are the only writes.
func ObserveExamples(ctx context.Context, p apidocs.Prober, examples []apidocs.Example, index map[string]string) ([]Snapshot, error) {
	operator, ok := p.Operator()
	if !ok {
		return nil, fmt.Errorf("the probe has no operator token")
	}
	ids := map[string]string{}
	var out []Snapshot
	for _, ex := range examples {
		path, capture := apidocs.Expand(ex.Path, ids)
		body := ex.Body
		for name, id := range ids {
			body = strings.ReplaceAll(body, "{{"+name+"}}", id)
		}
		credential := operator
		caller := "operator"
		if strings.Contains(path, "/edit/") ||
			strings.HasPrefix(path, "/api/v1/me/telegram-accounts") ||
			(ex.Method == http.MethodPut && strings.Contains(path, "/messages/")) {
			credential = p.Administrator()
			caller = "administrator"
		}
		rec := p.Do(ctx, ex.Method, path, credential.Authorization(), body)
		snap := Snapshot{
			Method: ex.Method, Path: path, Caller: caller,
			Status: rec.Code, Request: fmt.Sprintf("%s %s", ex.Method, path),
		}
		if capture != "" {
			id, err := apidocs.ExtractID(rec.Body.Bytes(), capture)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", snap.Request, err)
			}
			ids[capture] = id
		}
		if looksLikeJSON(rec.Body.Bytes()) {
			snap.JSON = true
			snap.Body = rec.Body.Bytes()
			var env struct {
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(snap.Body, &env) == nil && env.Error != nil {
				snap.Code = env.Error.Code
			}
		}
		if operationID := ids["operation_id"]; operationID != "" {
			snap.Path = strings.ReplaceAll(snap.Path, operationID, "edit_01EXAMPLE")
			snap.Request = strings.ReplaceAll(snap.Request, operationID, "edit_01EXAMPLE")
			snap.Body = []byte(strings.ReplaceAll(string(snap.Body), operationID, "edit_01EXAMPLE"))
		}
		if snap.Status == http.StatusOK || snap.Status == http.StatusCreated ||
			(snap.Status >= 400 && snap.Status < 500 && snap.Status != http.StatusNotFound && snap.Status != http.StatusMethodNotAllowed) {
			out = append(out, snap)
		}
	}
	if c, ok := ids["category"]; ok {
		_ = c
	}
	return out, nil
}

// SnapshotKey identifies the route a snapshot belongs to, for attaching it to
// the table entry.
func SnapshotKey(method, path string) string {
	for _, r := range apidocs.Routes {
		if apidocs.Matches(r.Pattern, path) {
			return method + " " + r.Pattern
		}
	}
	return method + " " + path
}
