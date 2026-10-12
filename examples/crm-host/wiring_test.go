package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm/schema"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/observability"
	"github.com/vislake/speed/pkg/tenant"

	"github.com/vislake/speed/pkg/authn"
	authnadmin "github.com/vislake/speed/pkg/authn/admin"
	"github.com/vislake/speed/pkg/authn/impersonation"
	"github.com/vislake/speed/pkg/authn/login/github"
	"github.com/vislake/speed/pkg/authn/login/google"
	"github.com/vislake/speed/pkg/authn/login/wechatmp"
	"github.com/vislake/speed/pkg/authn/login/wechatweb"
	"github.com/vislake/speed/pkg/authn/mfa/totp"
	"github.com/vislake/speed/pkg/authn/mfa/webauthn"
	"github.com/vislake/speed/pkg/authn/sso"
	ssooidc "github.com/vislake/speed/pkg/authn/sso/oidc"
	ssosaml "github.com/vislake/speed/pkg/authn/sso/saml"
)

// These cases read the descriptors and the host's tables instead of running the
// host, which is why they are not in e2e_test.go: the properties they state
// leave no trace in a run until the day they are broken, and then they leave
// the wrong one.

// embedsTenantModel reports whether a model embeds the scoping convention the
// way the tenant filter recognises it: tenant.Model as an anonymous field of
// the model itself, by value.
func embedsTenantModel(model any) bool {
	typ := reflect.TypeOf(model)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return false
	}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Anonymous && field.Type == reflect.TypeFor[tenant.Model]() {
			return true
		}
	}
	return false
}

// requiresCapability reports whether a descriptor declares a requirement on a
// capability. A token is a typed nil pointer, so comparing two of them compares
// the capability each designates.
func requiresCapability(module core.Module, capability core.Token) bool {
	for _, requirement := range module.Requires {
		if requirement.Token == capability {
			return true
		}
	}
	return false
}

// TestEveryModuleWritingAScopedTableRequiresTheRoster is the case for the one
// omission a command can make silently.
//
// The tenant filter is the roster module's plugin, and the database module
// installs a plugin only for the modules a command runs. A command that ran a
// module issuing statements on a scoped model and forgot the roster would run
// every one of them unscoped: an empty tenant written, every tenant's rows read,
// nothing refused and nothing reported. The requirement turns the omission into
// a startup failure at resolution, which is why it is asserted where it is
// written — no run shows it until the command that forgot is the one running.
func TestEveryModuleWritingAScopedTableRequiresTheRoster(t *testing.T) {
	t.Parallel()
	var scoped int
	for _, a := range applications {
		for _, model := range a.models {
			if !embedsTenantModel(model) {
				continue
			}
			scoped++
			if !requiresCapability(a.module, (*tenant.Catalog)(nil)) {
				t.Errorf("module %q issues statements on the scoped model %T and declares no requirement on %T, "+
					"so a command that forgot the roster would run them unscoped",
					a.module.Name, model, (*tenant.Catalog)(nil))
			}
		}
	}
	if scoped == 0 {
		t.Fatal("no application module lists a scoped model, so this case checks nothing")
	}
}

// TestEveryModuleThatLogsDeclaresTheLoggingDependency is the case design-log.md
// names: a module that takes a logger out of the registry declares the
// requirement.
//
// A module that took one without declaring it would be handed a logger all the
// same, as long as another module in the run declared it, and would look like
// every other module until that one was removed — then it would fail at startup
// in a module nobody changed. Every application module with a product holds a
// Named logger for its release records, which is this host's convention; one
// without a product has no logger to hold.
func TestEveryModuleThatLogsDeclaresTheLoggingDependency(t *testing.T) {
	t.Parallel()
	for _, a := range applications {
		if a.module.New == nil {
			continue
		}
		if !requiresCapability(a.module, (*log.Logger)(nil)) {
			t.Errorf("module %q builds a product, which holds a logger, and declares no requirement on %T",
				a.module.Name, (*log.Logger)(nil))
		}
	}
}

// The opposite assertion for the one capability whose instance is supplied and
// not built by a module's New. The reading surface is the config module's
// supplied instance: Execute supplies it before the drive begins, so it exists
// before any module is prepared, and core.Resolve takes it whatever a module
// declares. A requirement naming it would order the module after the config
// module, which has no callback, and take that module in, which every drive
// does already, so it adds nothing: a module that reads configuration resolves
// the surface and declares nothing for it.
func TestNoModuleDeclaresTheConfigReaderRequirement(t *testing.T) {
	t.Parallel()
	modules := []core.Module{hostModule()}
	for _, a := range applications {
		modules = append(modules, a.module)
	}
	for _, module := range modules {
		if requiresCapability(module, (*config.Reader)(nil)) {
			t.Errorf("module %q declares a requirement on %T, which no module needs to declare: "+
				"the reading surface is the config module's supplied instance and exists before any module is prepared",
				module.Name, (*config.Reader)(nil))
		}
	}
}

// The host module delivers nothing and constructs nothing, which is what keeps
// it out of every command: a module is taken into a command by being named
// there or by providing what a named module requires, and a module providing
// nothing can only arrive the first way.
func TestTheHostModuleProvidesNothingThatCouldPullItIntoACommand(t *testing.T) {
	t.Parallel()
	module := hostModule()
	if len(module.Provides) != 0 {
		t.Errorf("module %q declares %d provision(s); a capability delivered without an exclusive claim "+
			"draws its provider into every command that requires it", module.Name, len(module.Provides))
	}
	if module.New != nil {
		t.Errorf("module %q constructs something; it carries the host identity and has nothing to build", module.Name)
	}
}

// TestEveryStackNamesOneModuleOfEachKind is the case for the stack table. Each
// field names a module by its registered name, and a name in the wrong field —
// an event backend where the key-value one belongs — would assemble a command
// with no provider of one capability and two of another. The table is read
// against what each named module delivers.
func TestEveryStackNamesOneModuleOfEachKind(t *testing.T) {
	t.Parallel()
	kinds := []struct {
		field      string
		capability core.Token
		of         func(stack) string
	}{
		{"engine", (*db.Database)(nil), func(s stack) string { return s.engine }},
		{"kv", (*kv.KV)(nil), func(s stack) string { return s.kv }},
		{"event", (*event.Bus)(nil), func(s stack) string { return s.event }},
		{"exporter", (*observability.SpanExporter)(nil), func(s stack) string { return s.exporter }},
	}
	seen := map[string]bool{}
	for _, s := range stacks {
		if seen[s.name] {
			t.Errorf("two stacks are named %q", s.name)
		}
		seen[s.name] = true
		for _, kind := range kinds {
			name := kind.of(s)
			module, ok := core.ProcessRegistry.Lookup(name)
			if !ok {
				t.Errorf("stack %s names %q as its %s, and no module of that name is registered", s.name, name, kind.field)
				continue
			}
			if !provides(module, kind.capability) {
				t.Errorf("stack %s names %q as its %s, and it does not deliver %T", s.name, name, kind.field, kind.capability)
			}
		}
	}
}

// provides reports whether a descriptor delivers a capability.
func provides(module core.Module, capability core.Token) bool {
	for _, provision := range module.Provides {
		if provision.Token == capability {
			return true
		}
	}
	return false
}

// TestTheHarnessConfiguresEveryStack is the case for the harness keeping up
// with the table: every module a stack names has a backend here, so a stack
// added to the host is exercised rather than failed on a configuration nobody
// wrote.
func TestTheHarnessConfiguresEveryStack(t *testing.T) {
	t.Parallel()
	for _, s := range stacks {
		for _, name := range []string{s.engine, s.kv, s.event, s.exporter} {
			if _, ok := backends[name]; !ok {
				t.Errorf("stack %s names %q, which backends_test.go does not configure", s.name, name)
			}
		}
	}
}

// TestEveryModelNamesItsTable is the case for the tables the other cases derive
// from the models: a model whose table GORM would name by its own convention
// is one whose table no case can name in advance.
func TestEveryModelNamesItsTable(t *testing.T) {
	t.Parallel()
	for _, a := range applications {
		for _, model := range a.models {
			if _, ok := model.(interface{ TableName() string }); !ok {
				t.Errorf("module %q lists the model %T, which has no TableName method", a.module.Name, model)
			}
		}
	}
}

// tenantPackage is the import path of the scoping convention.
const tenantPackage = "github.com/vislake/speed/pkg/tenant"

// tokenPackage is the import path of the module this host wires to carry the
// access and refresh tokens.
const tokenPackage = "github.com/vislake/speed/pkg/authn/token"

// TestTheTokenModuleIsWiredAndReachesNoTenantPackage is the case for the edge
// this host gains with the token surface. Importing the package is the whole of
// the wiring, so some non-test file of this host has to import it — that is the
// positive control, without which an empty reading would pass. And the token
// package, like every package under pkg/authn, reaches no pkg/tenant package: the
// identity system sits below the tenant one, and the edge this host takes on must
// add no dependency from authn onto tenant.
func TestTheTokenModuleIsWiredAndReachesNoTenantPackage(t *testing.T) {
	t.Parallel()
	if !hostImports(t, ".", tokenPackage) {
		t.Errorf("no non-test file of this host imports %s, so the token surface is not wired", tokenPackage)
	}
	for _, name := range nonTestSources(t, filepath.Join("..", "..", "pkg", "authn", "token")) {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if p == tenantPackage || strings.HasPrefix(p, tenantPackage+"/") {
				t.Errorf("%s imports %s: the identity system must not reach the tenant one", name, p)
			}
		}
	}
}

// TestTheFactorModulesAreRegisteredWithTheirTables is the case for the two
// factor modules this host names on serve: each is registered, delivers the
// factor capability the multi-factor module takes all of with ResolveAll, and
// its migrations declare its own tables. The delivery runs where the modules are
// driven — a served case assembles them through the registry, and a module that
// declared a capability it did not deliver fails that drive with
// ErrUndeliveredCapability — so this case holds the names and the tables beside
// it.
//
// Registration follows the import graph: importing the package is what runs the
// init that registers the module, so the registration check passes however the
// names are written on serve. The two factors are parallel capability
// providers, which the dependency closure never picks on the host's behalf, so a
// name dropped from the serve Include list leaves the stack a factor short with
// no descriptor and no served case saying so — the second half of this case
// reads the names where they are written.
func TestTheFactorModulesAreRegisteredWithTheirTables(t *testing.T) {
	t.Parallel()
	for _, name := range []string{totp.ModuleName, webauthn.ModuleName} {
		module, ok := core.ProcessRegistry.Lookup(name)
		if !ok {
			t.Errorf("the module %q is not registered, so serve names a module nothing delivers", name)
			continue
		}
		if !provides(module, (*authn.Factor)(nil)) {
			t.Errorf("the module %q does not deliver %T", name, (*authn.Factor)(nil))
		}
	}
	onServe := modulesNamedOnServe(t)
	for _, module := range []string{
		"github.com/vislake/speed/pkg/authn/mfa/totp",
		"github.com/vislake/speed/pkg/authn/mfa/webauthn",
	} {
		if !onServe[module] {
			t.Errorf("serve names no module from %s, so the factor is registered but never runs", module)
		}
	}
	tables, err := migrationTables("sqlite")
	if err != nil {
		t.Fatalf("reading the declared migrations: %v", err)
	}
	for _, want := range []string{
		"authn_factors", "authn_totp_secrets", "authn_webauthn_users", "authn_webauthn_credentials",
	} {
		if !slices.Contains(tables, want) {
			t.Errorf("the declared migrations create no %q table; they create %v", want, tables)
		}
	}
}

// TestTheIdentityModulesAreRegisteredAndNamedOnServe is the case for the social
// and enterprise login methods, the administration routes and the impersonation
// records this host takes in beside the factor modules. Each is a module of the
// identity system named on serve, and none is drawn in by the dependency closure:
// the login methods are a non-exclusive seam the engine takes up with ResolveAll,
// and the enterprise provider register, the impersonation records and the
// administration module are exclusive providers or route mounts the host writes
// down one by one. A name dropped from the serve Include list leaves the stack a
// method, a capability or a route group short with no descriptor and no served
// case saying so, so this case holds both halves: the module is registered and
// delivers what its package declares, and serve names a module from that package.
func TestTheIdentityModulesAreRegisteredAndNamedOnServe(t *testing.T) {
	t.Parallel()
	deliveries := []struct {
		name       string
		importPath string
		capability core.Token
	}{
		{google.ModuleName, "github.com/vislake/speed/pkg/authn/login/google", (*authn.LoginMethod)(nil)},
		{github.ModuleName, "github.com/vislake/speed/pkg/authn/login/github", (*authn.LoginMethod)(nil)},
		{wechatweb.ModuleName, "github.com/vislake/speed/pkg/authn/login/wechatweb", (*authn.LoginMethod)(nil)},
		{wechatmp.ModuleName, "github.com/vislake/speed/pkg/authn/login/wechatmp", (*authn.LoginMethod)(nil)},
		{sso.ModuleName, "github.com/vislake/speed/pkg/authn/sso", (*authn.SSOProviders)(nil)},
		{ssosaml.ModuleName, "github.com/vislake/speed/pkg/authn/sso/saml", (*authn.LoginMethod)(nil)},
		{ssooidc.ModuleName, "github.com/vislake/speed/pkg/authn/sso/oidc", (*authn.LoginMethod)(nil)},
		{impersonation.ModuleName, "github.com/vislake/speed/pkg/authn/impersonation", (*authn.Impersonation)(nil)},
	}
	onServe := modulesNamedOnServe(t)
	for _, d := range deliveries {
		module, ok := core.ProcessRegistry.Lookup(d.name)
		if !ok {
			t.Errorf("the module %q is not registered, so serve names a module nothing delivers", d.name)
			continue
		}
		if !provides(module, d.capability) {
			t.Errorf("the module %q does not deliver %T", d.name, d.capability)
		}
		if !onServe[d.importPath] {
			t.Errorf("serve names no module from %s, so the module is registered but never runs", d.importPath)
		}
	}
	// The administration module delivers no capability of its own: it is taken in
	// by being named on serve, which is what mounts the management routes.
	if _, ok := core.ProcessRegistry.Lookup(authnadmin.ModuleName); !ok {
		t.Errorf("the module %q is not registered, so serve names a module nothing delivers", authnadmin.ModuleName)
	}
	if !onServe["github.com/vislake/speed/pkg/authn/admin"] {
		t.Error("serve names no module from the administration package, so the management routes are never mounted")
	}
}

// modulesNamedOnServe names the modules addStack's serve command takes in, each
// by the import path of the package that registers it.
//
// The command tree keeps its include lists unexported, so the set a served run
// assembles is not readable off the tree the way a descriptor is readable off
// core.ProcessRegistry: a name dropped from the serve Include list leaves no
// trace in a descriptor and none in a registry populated by the import graph.
// This reads the names where they are written, in the program's own source, so
// the case above holds serve to the names and not the import graph alone.
func modulesNamedOnServe(t *testing.T) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	declared := false
	for _, name := range nonTestSources(t, ".") {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		addStack := funcDeclNamed(file, "addStack")
		if addStack == nil {
			continue
		}
		declared = true
		serve := serveVariableIn(addStack)
		if serve == "" {
			t.Fatalf("%s declares addStack with no serve command bound to node.Sub(%s)", name, serveCommand)
		}
		ast.Inspect(addStack, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Include" {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok || receiver.Name != serve {
				return true
			}
			for _, arg := range call.Args {
				if module := moduleImportPath(file, arg); module != "" {
					named[module] = true
				}
			}
			return true
		})
	}
	if !declared {
		t.Fatal("no non-test file of the program declares addStack, so this case checks nothing")
	}
	if len(named) == 0 {
		t.Fatal("addStack names no module on serve, so this case checks nothing")
	}
	return named
}

// funcDeclNamed is the top-level function a file declares under name, or nil
// when it declares none.
func funcDeclNamed(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// serveVariableIn names the local a function binds the serve command to: the
// left side of the assignment whose right side is node.Sub(serveCommand). The
// argument is matched by the identifier's own text, which is the constant's
// name and not its value.
func serveVariableIn(fn *ast.FuncDecl) string {
	var name string
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		lhs, ok := assign.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sub" {
			return true
		}
		arg, ok := call.Args[0].(*ast.Ident)
		if !ok || arg.Name != "serveCommand" {
			return true
		}
		name = lhs.Name
		return false
	})
	return name
}

// moduleImportPath is the import path of the package that registers the module
// an Include argument names, when the argument is written as pkg.ModuleName; it
// is "" for any other argument.
func moduleImportPath(file *ast.File, arg ast.Expr) string {
	sel, ok := arg.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "ModuleName" {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(p)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if local == pkg.Name {
			return p
		}
	}
	return ""
}

// hostImports reports whether any non-test Go source directly under dir imports
// the named package.
func hostImports(t *testing.T, dir, importPath string) bool {
	t.Helper()
	names := nonTestSources(t, dir)
	if len(names) == 0 {
		t.Fatalf("no non-test Go source under %s, so this case checks nothing", dir)
	}
	for _, name := range names {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if importedAs(file, importPath) != "" {
			return true
		}
	}
	return false
}

// nonTestSources lists the non-test Go files directly under dir.
func nonTestSources(t *testing.T, dir string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("listing the Go source under %s: %v", dir, err)
	}
	var kept []string
	for _, name := range names {
		if !strings.HasSuffix(name, "_test.go") {
			kept = append(kept, name)
		}
	}
	return kept
}

// TestEveryScopedModelIsListed is the case for the one input the roster case
// above trusts: the models a module lists. It reads the program's own source,
// and every struct type that embeds the scoping convention has to be listed by
// some application module; one that is not is a scoped model the roster case
// never sees, and a command that runs its statements without the roster runs
// them unscoped with nothing said.
func TestEveryScopedModelIsListed(t *testing.T) {
	t.Parallel()
	listed := map[string]bool{}
	for _, a := range applications {
		for _, model := range a.models {
			typ := reflect.TypeOf(model)
			for typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			listed[typ.Name()] = true
		}
	}
	scoped, err := scopedTypesInSource(".")
	if err != nil {
		t.Fatalf("reading the program's source: %v", err)
	}
	if len(scoped) == 0 {
		t.Fatal("no type in the program's source embeds the scoping convention, so this case checks nothing")
	}
	for _, name := range scoped {
		if !listed[name] {
			t.Errorf("the type %s embeds %s.Model and no application module lists it among its models", name, tenantPackage)
		}
	}
}

// scopedTypesInSource names the struct types in the non-test Go files of a
// directory that embed the scoping convention, by value or by pointer.
func scopedTypesInSource(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var names []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		pkg := importedAs(file, tenantPackage)
		if pkg == "" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 && isSelector(field.Type, pkg, "Model") {
					names = append(names, spec.Name.Name)
				}
			}
			return true
		})
	}
	return names, nil
}

// importedAs is the name a file refers to an imported package by, or "" when
// the file does not import it.
func importedAs(file *ast.File, importPath string) string {
	for _, spec := range file.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil || p != importPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return path.Base(p)
	}
	return ""
}

// isSelector reports whether an expression is pkg.name, or a pointer to it.
func isSelector(expr ast.Expr, pkg, name string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && sel.Sel.Name == name
}

// TestEveryScopedManyToManyNamesTenantFirst is the case for the many2many
// declarations between scoped models, the one shape whose tenant scoping lives
// in a tag rather than in code.
//
// The tenant filter takes a join table's tenant column from where the scoped
// sides' keys write their tenant. The declaration the tenant design sanctions
// between two scoped models is the one whose joinForeignKey and joinReferences
// both name TenantID first, so both tenant halves land in one column; GORM's
// default writes one tenant column per side, from which no one tenant column
// can be taken, and the tenant module then refuses every statement on the join
// table. That refusal is met only by the case of the field that makes the
// declaration — today the stakeholder case and its one field — so this case
// holds every listed model's many2many field to another scoped model to the
// TenantID-first tag: a slice adding such a field is told here rather than at
// the first statement of its own case.
func TestEveryScopedManyToManyNamesTenantFirst(t *testing.T) {
	t.Parallel()
	var fields int
	for _, a := range applications {
		for _, model := range a.models {
			if !embedsTenantModel(model) {
				continue
			}
			owner := reflect.TypeOf(model)
			for owner.Kind() == reflect.Pointer {
				owner = owner.Elem()
			}
			for i := range owner.NumField() {
				field := owner.Field(i)
				settings := schema.ParseTagSetting(field.Tag.Get("gorm"), ";")
				joinTable, ok := settings["MANY2MANY"]
				if !ok {
					continue
				}
				target := field.Type
				for target.Kind() == reflect.Slice || target.Kind() == reflect.Pointer {
					target = target.Elem()
				}
				if !embedsTenantModel(reflect.New(target).Interface()) {
					continue
				}
				fields++
				for _, setting := range []string{"JOINFOREIGNKEY", "JOINREFERENCES"} {
					named := settings[setting]
					first, _, _ := strings.Cut(named, ",")
					if first != "TenantID" {
						t.Errorf("module %q lists %s, whose many2many %s names %q in its %s, so the tenant halves "+
							"of %s.%s's key do not land in one column of %s: the tenant module refuses every "+
							"statement on the join table. The setting has to name TenantID first",
							a.module.Name, owner.Name(), field.Name, named, setting, owner.Name(), field.Name, joinTable)
					}
				}
			}
		}
	}
	if fields == 0 {
		t.Fatal("no listed scoped model has a many2many field to another scoped model, so this case checks nothing")
	}
}

// TestEveryApplicationRunsOnACommandTheTreeHas is the case for the words the
// application table and the command groups carry. A module naming a command
// the tree does not have would run nowhere with nothing said, and a group
// sharing a word with another command would put two subtrees under one word.
func TestEveryApplicationRunsOnACommandTheTreeHas(t *testing.T) {
	t.Parallel()
	commands := []string{serveCommand, workerCommand}
	fixed := []string{serveCommand, workerCommand, "migrate"}
	for _, g := range commandGroups {
		if slices.Contains(fixed, g.word) || slices.Contains(commands, g.word) {
			t.Errorf("the command group %q shares its word with another command under every stack", g.word)
		}
		commands = append(commands, g.word)
	}
	for _, a := range applications {
		for _, command := range a.runsOn {
			if !slices.Contains(commands, command) {
				t.Errorf("module %q runs on %q, which is not a command under the stacks (%v)", a.module.Name, command, commands)
			}
		}
	}
}

// The statements the migration cases read out of a migration file. Comments
// are dropped first, so a comment that mentions a table references nothing.
var (
	sqlComment      = regexp.MustCompile(`--[^\n]*`)
	createTableStmt = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?"?([A-Za-z_][A-Za-z0-9_]*)"?`)
	referencesTable = regexp.MustCompile(`(?i)\bREFERENCES\s+"?([A-Za-z_][A-Za-z0-9_]*)"?`)
)

// migrationFiles reads every migration file a module declares, by path within
// its migration tree.
func migrationFiles(m db.Migrations) (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(m.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return err
		}
		body, err := fs.ReadFile(m.FS, p)
		if err != nil {
			return err
		}
		out[p] = sqlComment.ReplaceAllString(string(body), "")
		return nil
	})
	return out, err
}

// migrationTables names every table a registered module's migrations create on
// one dialect.
func migrationTables(dialect string) ([]string, error) {
	var tables []string
	for _, res := range core.Resources[db.Migrations](core.ProcessRegistry) {
		files, err := migrationFiles(res.Value)
		if err != nil {
			return nil, err
		}
		for p, body := range files {
			if strings.Split(p, "/")[0] != dialect {
				continue
			}
			for _, match := range createTableStmt.FindAllStringSubmatch(body, -1) {
				if table := strings.ToLower(match[1]); !slices.Contains(tables, table) {
					tables = append(tables, table)
				}
			}
		}
	}
	return tables, nil
}

// TestATableReferencingAnotherModulesTableIsOrderedAfterIt is the case for
// references between two modules' tables. The database module applies each
// module's migrations in the order the requirements give, with the module name
// breaking ties, so a migration referencing a table another module builds is
// applied after that module's only when it requires a capability that module
// delivers — without the requirement, the order is whatever the names say, and
// a name a slice picks can put its table first. The table token each
// table-owning module of this host delivers is the capability to require.
func TestATableReferencingAnotherModulesTableIsOrderedAfterIt(t *testing.T) {
	t.Parallel()
	owners := map[string]string{}
	declared := map[string]map[string]string{}
	for _, res := range core.Resources[db.Migrations](core.ProcessRegistry) {
		files, err := migrationFiles(res.Value)
		if err != nil {
			t.Fatalf("reading the migrations of %s: %v", res.Module, err)
		}
		if declared[res.Module] == nil {
			declared[res.Module] = map[string]string{}
		}
		for p, body := range files {
			declared[res.Module][p] = body
			for _, match := range createTableStmt.FindAllStringSubmatch(body, -1) {
				owners[strings.ToLower(match[1])] = res.Module
			}
		}
	}
	var references int
	for _, a := range applications {
		for p, body := range declared[a.module.Name] {
			for _, match := range referencesTable.FindAllStringSubmatch(body, -1) {
				table := strings.ToLower(match[1])
				// A host migration names no foreign key into the identity
				// system's own tables: those live in another module, a
				// cross-module reference would couple the two migrations'
				// order, and the account identifiers are checked through the
				// interfaces rather than by the database.
				if strings.HasPrefix(table, "authn_") {
					t.Errorf("module %q's migration %s references %s, an identity-system table; "+
						"the host's own migrations carry no foreign key into it", a.module.Name, p, table)
					continue
				}
				owner, ok := owners[table]
				switch {
				case !ok:
					t.Errorf("module %q's migration %s references %s, which no module's migrations create",
						a.module.Name, p, table)
					continue
				case owner == a.module.Name:
					continue
				}
				references++
				provider, _ := core.ProcessRegistry.Lookup(owner)
				if !ordersAfter(a.module, provider) {
					t.Errorf("module %q's migration %s references %s, which module %q builds, and %q requires "+
						"no capability %q delivers, so nothing orders the two migrations",
						a.module.Name, p, table, owner, a.module.Name, owner)
				}
			}
		}
	}
	if references == 0 {
		t.Fatal("no application module's migration references another module's table, so this case checks nothing")
	}
}

// ordersAfter reports whether a module declares a requirement on a capability
// another module delivers, which is what places its migrations after that
// module's.
func ordersAfter(dependent, provider core.Module) bool {
	for _, requirement := range dependent.Requires {
		if provides(provider, requirement.Token) {
			return true
		}
	}
	return false
}

// openAPIPath writes a path in gin's grammar the way an OpenAPI document writes
// it: each ":name" segment is "{name}".
func openAPIPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if name, ok := strings.CutPrefix(segment, ":"); ok {
			segments[i] = "{" + name + "}"
		}
	}
	return strings.Join(segments, "/")
}

// TestEveryJournalledRouteIsAnOperationItsModuleDocuments is the case for the one
// mistake a journal entry can make silently. The journal compares a request's
// method and the path of the route it was matched to with each entry of its
// requests item, character for character, so an entry that equals no
// registered route is well formed and never matches: the rows it was written to
// bring never appear, and nothing says so. A module's OpenAPI fragments document
// the operations its routes serve, under the OpenAPI spelling of each path, so
// an entry that names no documented operation names no route; and an entry
// still written in the OpenAPI spelling is documented under its own text and
// reaches no route either, which is why it is refused outright.
func TestEveryJournalledRouteIsAnOperationItsModuleDocuments(t *testing.T) {
	t.Parallel()
	var checked int
	for _, a := range applications {
		documented := map[string]bool{}
		for _, res := range a.module.Resources {
			spec, ok := res.(speedhttp.Spec)
			if !ok {
				continue
			}
			var part fragment
			if err := json.Unmarshal(spec.Document, &part); err != nil {
				t.Fatalf("reading the fragment %s declares for %s: %v", a.module.Name, spec.Endpoint, err)
			}
			for path, item := range part.Paths {
				for method := range item {
					documented[strings.ToUpper(method)+" "+path] = true
				}
			}
		}
		for _, route := range a.journalled {
			checked++
			if strings.ContainsAny(route, "{}") {
				t.Errorf("%s hands the journal %q, which writes the path in the OpenAPI spelling; "+
					"the journal compares the path as the route was registered, with :name for a parameter",
					a.module.Name, route)
				continue
			}
			method, path := splitRoute(route)
			if !documented[method+" "+openAPIPath(path)] {
				t.Errorf("%s hands the journal %q, and none of its OpenAPI fragments documents %s %s",
					a.module.Name, route, method, openAPIPath(path))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no module hands the journal a route, so this case checks nothing")
	}
}
