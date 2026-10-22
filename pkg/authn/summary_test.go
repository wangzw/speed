package authn_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This file is the cross-module summary case the events design calls for: the
// closed sets it fixes (the audit detail key names, the instruments and their
// attribute keys and values) are held against every module of the capability
// directory at once, by reading the sources. Each module's own case holds its
// own set; a module that adds a key or a value outside the design's set has no
// own case to catch it, and this one does, because it aggregates over the tree
// instead of over one module's table.
//
// The sets below are transcribed from design-authn-events.md. They are the
// contract, so they are written out here and not derived from the code under
// test; only what the code uses is read from the source.

// auditDetailKeys is the design's closed set of audit detail key names.
var auditDetailKeys = []string{
	"method", "delivery", "reason_class", "sid", "peer", "identifier_mac", "factor_id",
	"kind", "scope", "by", "tenant", "change", "provider", "ttl_seconds",
	"parent_sid", "secret_id", "source", "on_behalf_of", "impersonation_scope",
	"session", "grant_id", "action_id",
}

// metricAttributeKeys is the union of the attribute keys of the design's
// instrument table.
var metricAttributeKeys = []string{
	"outcome", "method", "factor", "scope", "operation", "protocol",
	"algorithm", "hook", "mandatory",
}

// metricInstruments is the design's instrument names, Meter authn. The
// admission counter is tenant-owned and is not in this set.
var metricInstruments = []string{
	"authn.gate.conclusions", "authn.login.completions", "authn.password.verifications",
	"authn.password.derive.duration", "authn.mfa.verifications", "authn.session.operations",
	"authn.token.operations", "authn.throttle.engagements", "authn.sms.sends",
	"authn.sso.assertions", "authn.impersonation.sessions", "authn.audit.write-failures",
	"authn.cleanup.failures",
}

// metricAttributeValues is the union, per attribute key, of the closed value
// sets of the design's instrument table. A value belongs if the key it is
// reported under lists it in any row.
var metricAttributeValues = map[string][]string{
	"outcome": {
		"unspoken", "authenticated", "denied", "unavailable", "anonymous",
		"success", "failed", "throttled", "refused", "expired",
		"ok", "bad", "cached", "busy", "upgraded",
		"replay", "invalid", "reuse-detected",
		"sent", "uncertain", "rejected", "unroutable", "not-sendable",
		"unsupported-region", "budget-exhausted", "suppressed", "dropped",
		"accepted", "signature", "audience", "time", "in-response-to", "issuer", "destination", "other",
		"started", "ended",
	},
	"method":    {"pwd", "sms", "otp", "webauthn", "oauth", "oidc", "saml", "static", "client-secret"},
	"factor":    {"totp", "sms", "webauthn"},
	"scope":     {"identifier", "account", "factor", "phone", "client", "signup-username"},
	"operation": {"created", "elevated", "revoked", "revoked-all", "purged", "issued", "refreshed", "client-credentials"},
	"protocol":  {"saml", "oidc"},
	"algorithm": {"argon2id", "pbkdf2-sha256"},
	"hook":      {"session", "mfa", "sms", "secret", "other"},
	"mandatory": {"true", "false"},
}

// The roots the case reads. The audit and attribute scans take the authn tree
// and the one tenant package that writes an authn audit row; the instrument
// scan takes the authn tree alone, since the tenant counter is not authn's.
var (
	auditAttrRoots     = []string{".", "../tenant/admission"}
	instrumentRoots    = []string{"."}
	attributeFuncs     = map[string]bool{"String": true, "Bool": true, "Int": true, "Int64": true, "Float64": true}
	instrumentSuffixes = []string{"Counter", "Histogram", "Gauge"}
)

func setOf(vs []string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}

// scanner reads the authn tree and holds what it finds.
type scanner struct {
	t         *testing.T
	fset      *token.FileSet
	files     int
	dirConst  map[string]map[string]string // dir -> const name -> value
	globals   map[string]string            // const name -> value, across dirs
	callSites []callSite
	funcs     map[string][]*ast.FuncDecl

	detailKeys map[string]string // detail key used -> a site that used it
	detailNote []string          // sites whose key could not be resolved
	attrKeys   map[string]string
	attrVals   []attrValue
	instr      map[string]string

	// A directory that holds a secret configuration value, and one that ships a
	// sentinel case.
	sensitiveDirs map[string]bool
	secrecyDirs   map[string]bool

	rawConst map[string]map[string]ast.Expr
}

type callSite struct {
	dir  string
	name string
	args []ast.Expr
}

type attrValue struct {
	key, value, site string
}

func newScanner(t *testing.T) *scanner {
	return &scanner{
		t:             t,
		fset:          token.NewFileSet(),
		dirConst:      map[string]map[string]string{},
		globals:       map[string]string{},
		funcs:         map[string][]*ast.FuncDecl{},
		detailKeys:    map[string]string{},
		attrKeys:      map[string]string{},
		instr:         map[string]string{},
		sensitiveDirs: map[string]bool{},
		secrecyDirs:   map[string]bool{},
		rawConst:      map[string]map[string]ast.Expr{},
	}
}

// parse reads every non-test Go file under each root and keeps the tree.
func (s *scanner) parse(roots []string) map[string][]*ast.File {
	byDir := map[string][]*ast.File{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			if strings.HasSuffix(p, "_test.go") {
				if filepath.Base(p) == "secrecy_test.go" {
					s.secrecyDirs[filepath.Dir(p)] = true
				}
				return nil
			}
			f, perr := parser.ParseFile(s.fset, p, nil, 0)
			if perr != nil {
				return perr
			}
			dir := filepath.Dir(p)
			byDir[dir] = append(byDir[dir], f)
			s.files++
			return nil
		})
		if err != nil {
			s.t.Fatalf("walking %s: %v", root, err)
		}
	}
	return byDir
}

// collectConsts gathers every const declaration per directory and settles the
// names that resolve to a string literal, a few passes so that a constant built
// from another constant settles too.
func (s *scanner) collectConsts(byDir map[string][]*ast.File) {
	for dir, files := range byDir {
		s.rawConst[dir] = map[string]ast.Expr{}
		for _, f := range files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, n := range vs.Names {
						if i < len(vs.Values) {
							s.rawConst[dir][n.Name] = vs.Values[i]
						}
					}
				}
			}
		}
	}
	for pass := 0; pass < 8; pass++ {
		for dir := range s.rawConst {
			if s.dirConst[dir] == nil {
				s.dirConst[dir] = map[string]string{}
			}
			for name, expr := range s.rawConst[dir] {
				if _, ok := s.dirConst[dir][name]; ok {
					continue
				}
				if v, ok := s.resolveIn(expr, dir); ok {
					s.dirConst[dir][name] = v
				}
			}
		}
		for _, cs := range s.dirConst {
			for name, v := range cs {
				if _, ok := s.globals[name]; !ok {
					s.globals[name] = v
				}
			}
		}
	}
}

// resolveIn resolves a constant expression in dir: a string literal, a name in
// the directory's constants, a name of any directory's constants, a selector's
// name, a string conversion or a concatenation of the above.
func (s *scanner) resolveIn(expr ast.Expr, dir string) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			v, err := strconv.Unquote(e.Value)
			if err != nil {
				return "", false
			}
			return v, true
		}
	case *ast.Ident:
		// A bare name can only be a name of the same package's constants: a
		// name from another package is always a selector. Resolving a bare name
		// against every package's constants would read a parameter as the value
		// of some unrelated module's constant of the same name.
		if v, ok := s.dirConst[dir][e.Name]; ok {
			return v, true
		}
	case *ast.SelectorExpr:
		if v, ok := s.globals[e.Sel.Name]; ok {
			return v, true
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			a, ok1 := s.resolveIn(e.X, dir)
			b, ok2 := s.resolveIn(e.Y, dir)
			if ok1 && ok2 {
				return a + b, true
			}
		}
	case *ast.CallExpr:
		if len(e.Args) == 1 && isStringConvert(e) {
			return s.resolveIn(e.Args[0], dir)
		}
	case *ast.ParenExpr:
		return s.resolveIn(e.X, dir)
	}
	return "", false
}

// resolve resolves a name or literal that stands for itself at a use site, the
// same way a constant is resolved but falling back across the directory map.
func (s *scanner) resolve(expr ast.Expr, dir string) (string, bool) {
	return s.resolveIn(expr, dir)
}

func isStringConvert(c *ast.CallExpr) bool {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return f.Name == "string"
	case *ast.SelectorExpr:
		return f.Sel.Name == "string"
	}
	return false
}

// collectFuncsAndCalls records every function declaration and every call in the
// tree, for tracing a value that reaches an attribute through a parameter.
func (s *scanner) collectFuncsAndCalls(byDir map[string][]*ast.File) {
	for dir, files := range byDir {
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.FuncDecl:
					s.funcs[v.Name.Name] = append(s.funcs[v.Name.Name], v)
				case *ast.CallExpr:
					if name := calleeName(v.Fun); name != "" {
						s.callSites = append(s.callSites, callSite{dir: dir, name: name, args: v.Args})
					}
				}
				return true
			})
		}
	}
}

func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// scan walks one parsed file, tracking the enclosing function so that a value
// passed through a parameter can be traced to the call sites.
func (s *scanner) scan(byDir map[string][]*ast.File) {
	for dir, files := range byDir {
		for _, f := range files {
			ast.Walk(&walker{s: s, dir: dir, file: f}, f)
		}
	}
}

type walker struct {
	s    *scanner
	dir  string
	file *ast.File
	fn   *ast.FuncDecl
}

func (w *walker) Visit(n ast.Node) ast.Visitor {
	switch v := n.(type) {
	case *ast.FuncDecl:
		return &walker{s: w.s, dir: w.dir, file: w.file, fn: v}
	case *ast.CallExpr:
		w.s.attributeCall(v, w.dir, w.fn)
		w.s.instrumentCall(v, w.dir)
		w.s.literalRegisterCall(v, w.dir)
	case *ast.CompositeLit:
		w.s.detailLiteral(v, w.dir, w.fn)
		w.s.sensitiveLiteral(v, w.dir)
	case *ast.AssignStmt:
		w.s.detailAssign(v, w.dir)
	}
	return w
}

// sensitiveLiteral marks a directory as holding a secret configuration value: a
// config item whose Sensitive field is set.
func (s *scanner) sensitiveLiteral(lit *ast.CompositeLit, dir string) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok || id.Name != "Sensitive" {
			continue
		}
		if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "true" {
			s.sensitiveDirs[dir] = true
		}
	}
}

// literalRegisterCall marks a directory that registers a secret literal for
// redaction, which is the other way a module holds a secret value.
func (s *scanner) literalRegisterCall(call *ast.CallExpr, dir string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Register" {
		return
	}
	if x, ok := sel.X.(*ast.Ident); ok && x.Name == "literal" {
		s.sensitiveDirs[dir] = true
	}
}

// attributeCall holds an attribute constructor call against the design: the
// key it reports under is in the closed set, and a value that can be read from
// the source is in the key's value set.
func (s *scanner) attributeCall(call *ast.CallExpr, dir string, fn *ast.FuncDecl) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "attribute" || !attributeFuncs[sel.Sel.Name] {
		return
	}
	if len(call.Args) < 2 {
		return
	}
	site := s.fset.Position(call.Pos()).String()
	key, ok := s.resolve(call.Args[0], dir)
	if !ok {
		s.attrKeys["<unresolved "+site+">"] = site
		return
	}
	s.attrKeys[key] = site
	for _, v := range s.valuesOf(call.Args[1], dir, fn) {
		s.attrVals = append(s.attrVals, attrValue{key: key, value: v, site: site})
	}
}

// valuesOf reads the values an attribute could be reported with. A value that
// is not a name bound to a constant is not read: it is either a computed value
// or one that arrives from data, and only what the source can name is held.
func (s *scanner) valuesOf(expr ast.Expr, dir string, fn *ast.FuncDecl) []string {
	if c, ok := expr.(*ast.CallExpr); ok && isStringConvert(c) && len(c.Args) == 1 {
		return s.valuesOf(c.Args[0], dir, fn)
	}
	if v, ok := s.resolve(expr, dir); ok {
		return []string{v}
	}
	id, ok := expr.(*ast.Ident)
	if !ok || fn == nil {
		return nil
	}
	idx, ok := paramIndex(fn, id.Name)
	if !ok {
		return nil
	}
	nparams := len(paramNames(fn.Type.Params))
	var out []string
	for _, cs := range s.callSites {
		// The call must be in the function's own package: a same-named function
		// elsewhere is another function entirely, and its arguments are not this
		// one's.
		if cs.dir != dir || cs.name != fn.Name.Name || len(cs.args) != nparams || idx >= len(cs.args) {
			continue
		}
		if v, ok := s.resolve(cs.args[idx], cs.dir); ok {
			out = append(out, v)
		}
	}
	return out
}

// instrumentCall holds an instrument-creation call's name against the design's
// instrument set.
func (s *scanner) instrumentCall(call *ast.CallExpr, dir string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	metricMethod := false
	for _, suffix := range instrumentSuffixes {
		if strings.HasSuffix(sel.Sel.Name, suffix) {
			metricMethod = true
			break
		}
	}
	if !metricMethod || len(call.Args) < 1 {
		return
	}
	if v, ok := s.resolve(call.Args[0], dir); ok {
		s.instr[v] = s.fset.Position(call.Pos()).String()
	}
}

// detailLiteral holds the keys of a map literal that is the Detail of an audit
// entry, whether written inline, through a variable, or returned by a helper.
func (s *scanner) detailLiteral(lit *ast.CompositeLit, dir string, fn *ast.FuncDecl) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok || id.Name != "Detail" {
			continue
		}
		s.addDetailValue(kv.Value, dir, fn)
	}
}

// addDetailValue records the keys a Detail expression carries.
func (s *scanner) addDetailValue(value ast.Expr, dir string, fn *ast.FuncDecl) {
	site := s.fset.Position(value.Pos()).String()
	switch v := value.(type) {
	case *ast.CompositeLit:
		s.addMapKeys(v, dir, site)
	case *ast.Ident:
		if !s.addVarKeys(v.Name, dir, fn) {
			// A parameter of a helper that builds the entry: the callers hand
			// the map in, so read the argument they pass.
			s.addParamArgDetails(fn, v.Name, dir)
		}
	case *ast.CallExpr:
		name := calleeName(v.Fun)
		for _, decl := range s.funcs[name] {
			if decl.Body == nil {
				continue
			}
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				if ret, ok := n.(*ast.ReturnStmt); ok {
					for _, r := range ret.Results {
						if cl, ok := r.(*ast.CompositeLit); ok {
							s.addMapKeys(cl, dir, site)
						}
					}
				}
				return true
			})
		}
	}
}

// detailAssign reads a map written to a variable or an index whose name marks
// it as a detail, so a detail built in two statements is read as well as one
// written inline.
func (s *scanner) detailAssign(as *ast.AssignStmt, dir string) {
	for i, lhs := range as.Lhs {
		switch l := lhs.(type) {
		case *ast.Ident:
			if !strings.Contains(strings.ToLower(l.Name), "detail") {
				continue
			}
			if i < len(as.Rhs) {
				if cl, ok := as.Rhs[i].(*ast.CompositeLit); ok {
					s.addMapKeys(cl, dir, s.fset.Position(cl.Pos()).String())
				}
			}
		case *ast.IndexExpr:
			base, ok := l.X.(*ast.Ident)
			if !ok || !strings.Contains(strings.ToLower(base.Name), "detail") {
				continue
			}
			s.recordDetailKey(l.Index, dir, s.fset.Position(l.Index.Pos()).String())
		}
	}
}

// addVarKeys records the keys of a map literal assigned to name in fn's body.
func (s *scanner) addVarKeys(name, dir string, fn *ast.FuncDecl) bool {
	if fn == nil || fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || id.Name != name {
				continue
			}
			if i < len(as.Rhs) {
				if cl, ok := as.Rhs[i].(*ast.CompositeLit); ok {
					s.addMapKeys(cl, dir, s.fset.Position(cl.Pos()).String())
					found = true
				}
			}
		}
		return true
	})
	// Index writes on the variable.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			idx, ok := lhs.(*ast.IndexExpr)
			if !ok {
				continue
			}
			base, ok := idx.X.(*ast.Ident)
			if ok && base.Name == name {
				s.recordDetailKey(idx.Index, dir, s.fset.Position(idx.Index.Pos()).String())
				found = true
			}
		}
		return true
	})
	return found
}

// addParamArgDetails reads the map literal that callers pass for a helper's
// detail parameter.
func (s *scanner) addParamArgDetails(fn *ast.FuncDecl, param, dir string) {
	if fn == nil {
		return
	}
	idx, ok := paramIndex(fn, param)
	if !ok {
		return
	}
	nparams := len(paramNames(fn.Type.Params))
	for _, cs := range s.callSites {
		if cs.dir != dir || cs.name != fn.Name.Name || len(cs.args) != nparams || idx >= len(cs.args) {
			continue
		}
		if cl, ok := cs.args[idx].(*ast.CompositeLit); ok {
			s.addMapKeys(cl, cs.dir, s.fset.Position(cl.Pos()).String())
		}
	}
}

// addMapKeys records every key of a map literal.
func (s *scanner) addMapKeys(lit *ast.CompositeLit, dir, site string) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		s.recordDetailKey(kv.Key, dir, site)
	}
}

// recordDetailKey resolves one key expression and records it, or notes that it
// could not be read.
func (s *scanner) recordDetailKey(expr ast.Expr, dir, site string) {
	if v, ok := s.resolve(expr, dir); ok {
		s.detailKeys[v] = site
		return
	}
	s.detailNote = append(s.detailNote, site)
}

func paramNames(fl *ast.FieldList) []string {
	var out []string
	if fl == nil {
		return out
	}
	for _, f := range fl.List {
		for _, n := range f.Names {
			out = append(out, n.Name)
		}
		if len(f.Names) == 0 {
			out = append(out, "")
		}
	}
	return out
}

func paramIndex(fn *ast.FuncDecl, name string) (int, bool) {
	for i, p := range paramNames(fn.Type.Params) {
		if p == name {
			return i, true
		}
	}
	return 0, false
}

// TestTheSummaryScanReachedTheTree is the guard the rest of the file rests on:
// a scan that silently read nothing would pass every case below.
func TestTheSummaryScanReachedTheTree(t *testing.T) {
	s := newScanner(t)
	byDir := s.parse(auditAttrRoots)
	s.collectConsts(byDir)
	s.collectFuncsAndCalls(byDir)
	s.scan(byDir)

	if s.files < 50 {
		t.Fatalf("the scan read %d files, which is less than the authn tree holds", s.files)
	}
	if len(s.detailKeys) < 10 {
		t.Fatalf("the scan resolved %d audit detail keys, want the design's set read from the sources", len(s.detailKeys))
	}
	if len(s.attrKeys) < 5 {
		t.Fatalf("the scan resolved %d metric attribute keys, want them read from the sources", len(s.attrKeys))
	}
	if len(s.instr) < 10 {
		t.Fatalf("the scan resolved %d instrument names, want the design's set read from the sources", len(s.instr))
	}
}

// TestEveryAuditDetailKeyIsInTheDesignsSet holds the first cross-module
// discipline: whatever key any module of the tree writes into an audit row's
// detail belongs to the design's closed set.
func TestEveryAuditDetailKeyIsInTheDesignsSet(t *testing.T) {
	s := newScanner(t)
	byDir := s.parse(auditAttrRoots)
	s.collectConsts(byDir)
	s.collectFuncsAndCalls(byDir)
	s.scan(byDir)

	allowed := setOf(auditDetailKeys)
	for key, site := range s.detailKeys {
		if !allowed[key] {
			t.Errorf("the audit detail key %q at %s is outside the design's closed set", key, site)
		}
	}
	for _, site := range s.detailNote {
		t.Errorf("the audit detail key at %s is not a name or a literal the case can read", site)
	}

	// The module-declared detail key constants are read too, so a constant
	// written with an off-set value fails even where no literal site shows it.
	seen := 0
	for dir, consts := range s.dirConst {
		for name, value := range consts {
			if !strings.Contains(strings.ToLower(name), "detail") {
				continue
			}
			seen++
			if !allowed[value] {
				t.Errorf("the constant %s in %s is %q, outside the design's detail key set", name, dir, value)
			}
		}
	}
	if seen == 0 {
		t.Error("no detail key constant was read, so the constants are not held")
	}
}

// TestEveryMetricAttributeKeyIsInTheDesignsSet holds the attribute keys the
// tree reports under against the design's set.
func TestEveryMetricAttributeKeyIsInTheDesignsSet(t *testing.T) {
	s := newScanner(t)
	byDir := s.parse(auditAttrRoots)
	s.collectConsts(byDir)
	s.collectFuncsAndCalls(byDir)
	s.scan(byDir)

	allowed := setOf(metricAttributeKeys)
	for key, site := range s.attrKeys {
		if !allowed[key] {
			t.Errorf("the metric attribute key %q at %s is outside the design's closed set", key, site)
		}
	}
}

// TestEveryMetricInstrumentIsInTheDesignsSet holds the instrument names the
// authn tree registers against the design's set.
func TestEveryMetricInstrumentIsInTheDesignsSet(t *testing.T) {
	s := newScanner(t)
	byDir := s.parse(instrumentRoots)
	s.collectConsts(byDir)
	s.collectFuncsAndCalls(byDir)
	s.scan(byDir)

	allowed := setOf(metricInstruments)
	for name, site := range s.instr {
		if !allowed[name] {
			t.Errorf("the instrument %q at %s is outside the design's closed set", name, site)
		}
	}
}

// TestEveryMetricAttributeValueIsInItsDesignsSet holds the values that can be
// read from the sources against the value set of the key they are reported
// under. A value that arrives from data or a computation is not read; what is
// read is what the source names, and it must be in the set.
func TestEveryMetricAttributeValueIsInItsDesignsSet(t *testing.T) {
	s := newScanner(t)
	byDir := s.parse(auditAttrRoots)
	s.collectConsts(byDir)
	s.collectFuncsAndCalls(byDir)
	s.scan(byDir)

	if len(s.attrVals) < 5 {
		t.Fatalf("the scan read %d attribute values, want the constant-fed ones read from the call sites", len(s.attrVals))
	}
	for _, av := range s.attrVals {
		set, ok := metricAttributeValues[av.key]
		if !ok {
			continue // the key itself is held by the attribute-key case
		}
		if !setOf(set)[av.value] {
			t.Errorf("the value %q of attribute %q at %s is outside the design's set for that key", av.value, av.key, av.site)
		}
	}
}

// TestEveryModuleHoldingASecretShipsASentinelTest holds the third cross-module
// discipline: the design says a module that holds a secret configuration value
// registers the value as a literal matcher and asserts, through a test over its
// error paths, that the configured sentinel never reaches the output. A module
// that holds a secret and ships no such test is a module the discipline does not
// reach, and the design says the discipline has no mechanism behind it but those
// tests. Whether a module's secret value does reach an output needs a running
// module with a configured secret, which a source read cannot do; what a source
// read can hold is that the case exists for every module that holds a secret.
func TestEveryModuleHoldingASecretShipsASentinelTest(t *testing.T) {
	s := newScanner(t)
	byDir := s.parse(auditAttrRoots)
	s.collectConsts(byDir)
	s.collectFuncsAndCalls(byDir)
	s.scan(byDir)

	if len(s.sensitiveDirs) == 0 {
		t.Fatal("no module holding a secret configuration value was found; the case cannot hold anything")
	}
	for dir := range s.sensitiveDirs {
		if !s.secrecyDirs[dir] {
			t.Errorf("the module in %s holds a secret configuration value but ships no secrecy_test.go, "+
				"so the sentinel-not-in-the-output discipline is not applied to it", dir)
		}
	}
}
