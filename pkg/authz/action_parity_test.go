package authz_test

import (
	"go/ast"
	"go/build"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/core"
)

// The guard holds the two vocabularies together: authn.Action, the actions the
// authentication side asks a Decider about, and authz.Permission, the
// permission constants this capability stores. The action's string is the
// permission's string, the one translation maps each permission to the engine's
// (object, action) pair, and the catalog declares every permission. Both lists
// are read from the packages' own sources, so a constant of either type added on
// either side is seen here without a second list to keep in step.
//
// The guard runs from package authz_test because it imports subpackages of this
// directory, which import the root package: an internal test of the root could
// not import them without a cycle.

// thisDir is this test file's directory, the root package pkg/authz. It is read
// from the file's own location so that the guard reads the sources wherever go
// test is run from.
func thisDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file")
	}
	return filepath.Dir(file)
}

// declaredConsts reads the non-test Go files of dir, type-checks the package,
// and returns every string constant whose type is the named type, keyed by the
// constant's name. The type is read from the type checker rather than from the
// declaration's syntax, so a constant of the type is seen whatever shape its
// declaration takes — an explicit type, a conversion, or a value the compiler
// folds from a longer expression. The type's own name is the filter, so a
// constant of another type — an untagged string, a Domain — is not part of the
// vocabulary: an untyped constant carries an untyped type and a plain string
// carries string, and neither is the named type.
func declaredConsts(t *testing.T, dir, typeName string) map[string]string {
	t.Helper()
	pkg, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(pkg.GoFiles))
	for _, name := range pkg.GoFiles {
		file, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files = append(files, file)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	checked, err := conf.Check(pkg.Name, fset, files, nil)
	if err != nil {
		t.Fatalf("type-checking %s: %v", dir, err)
	}
	found := map[string]string{}
	scope := checked.Scope()
	for _, name := range scope.Names() {
		obj, ok := scope.Lookup(name).(*types.Const)
		if !ok || !isNamedType(obj.Type(), typeName) {
			continue
		}
		found[name] = constant.StringVal(obj.Val())
	}
	return found
}

// isNamedType reports whether typ is the named type typeName. An untyped
// constant carries an untyped type and a constant of a plain string carries
// string, so neither matches.
func isNamedType(typ types.Type, typeName string) bool {
	named, ok := types.Unalias(typ).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Name() == typeName
}

// actionVocabulary reads the authn package's Action constants, and
// permissionVocabulary the root package's Permission constants.
func actionVocabulary(t *testing.T) map[string]string {
	t.Helper()
	return declaredConsts(t, filepath.Join(thisDir(t), "..", "authn"), "Action")
}

func permissionVocabulary(t *testing.T) map[string]string {
	t.Helper()
	return declaredConsts(t, thisDir(t), "Permission")
}

// sortedNames gives a map's keys in order, so a failure message and a counted
// comparison do not depend on map iteration order.
func sortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Each authn.Action has a Permission constant of the same string, and each
// permission whose string names an action names one that exists: the two
// vocabularies are one, neither missing a counterpart nor carrying an extra one.
// The translation covers every action as well, so an action a Decider is asked
// about is one it can put to the engine.
func TestEveryActionHasAMatchingPermission(t *testing.T) {
	actions := actionVocabulary(t)
	permissions := permissionVocabulary(t)

	// Positive controls: the reading found the packages and their constants, so
	// the comparisons below judge something.
	if got := actions["ActionTenantAccess"]; got != string(authn.ActionTenantAccess) {
		t.Fatalf("the authn source declares ActionTenantAccess as %q, want %q", got, authn.ActionTenantAccess)
	}
	if got := permissions["PermissionAccountRead"]; got != string(authz.PermissionAccountRead) {
		t.Fatalf("the authz source declares PermissionAccountRead as %q, want %q", got, authz.PermissionAccountRead)
	}
	if len(actions) == 0 || len(permissions) == 0 {
		t.Fatalf("the vocabularies are empty: %d actions, %d permissions", len(actions), len(permissions))
	}

	permissionStrings := make(map[string]bool, len(permissions))
	for _, value := range permissions {
		permissionStrings[value] = true
	}
	actionStrings := make(map[string]bool, len(actions))
	for _, value := range actions {
		actionStrings[value] = true
	}

	for _, name := range sortedNames(actions) {
		value := actions[name]
		if !permissionStrings[value] {
			t.Errorf("authn.%s (%q) has no permission constant with the same string", name, value)
		}
		if object, action, ok := translation.Lookup(authz.Permission(value)); !ok || object == "" || action == "" {
			t.Errorf("the translation holds no row for authn.%s (%q): got (%q, %q, %t)",
				name, value, object, action, ok)
		}
	}
	for _, name := range sortedNames(permissions) {
		value := permissions[name]
		if !strings.HasPrefix(value, "authn.") {
			continue
		}
		if !actionStrings[value] {
			t.Errorf("authz.%s (%q) names no authn.Action", name, value)
		}
	}
}

// Every permission constant has exactly one row in the guard's translation, and
// that row's object and action are non-empty. The translation is the single
// place a permission becomes the engine's (object, action) pair, so a permission
// with no row is a decision the module cannot express, and the guard fails the
// build until the row is added.
func TestTheTranslationCoversEveryPermission(t *testing.T) {
	permissions := permissionVocabulary(t)
	if len(permissions) == 0 {
		t.Fatal("the permission vocabulary is empty, so the translation was held to nothing")
	}
	for _, name := range sortedNames(permissions) {
		value := permissions[name]
		object, action, ok := translation.Lookup(authz.Permission(value))
		if !ok {
			t.Errorf("authz.%s (%q) has no translation row", name, value)
			continue
		}
		if object == "" || action == "" {
			t.Errorf("the row for authz.%s (%q) is (%q, %q), want a non-empty object and action",
				name, value, object, action)
		}
	}
}

// The catalog is the union of the permission specs the registry holds. The
// policy module's own spec is the capability's vocabulary: it declares every
// permission constant and nothing else, one-to-one. A module may declare a
// permission of its own beside them, and every declared permission — the
// constants and a module's own alike — must carry a translation row, because a
// permission the translation cannot express is one no route may name and no
// grant can hold.
//
// The catalog's declaration is read from the module the way its own build reads
// it, rather than by driving the module: the module's construction needs a
// database, and what is under guard here is the vocabulary and not the store.
func TestTheCatalogDeclaresEveryPermission(t *testing.T) {
	permissions := permissionVocabulary(t)

	reg := core.New()
	reg.Register(policy.Module())
	own := catalogOf(t, reg)
	if len(own) == 0 {
		t.Fatal("the policy module declares no permission spec, so the catalog holds nothing to compare")
	}

	constantValues := map[string]bool{}
	for _, value := range permissions {
		constantValues[value] = true
	}
	for _, name := range sortedNames(permissions) {
		if value := permissions[name]; !own[value] {
			t.Errorf("the catalog does not declare the permission %s (%q)", name, value)
		}
	}
	for _, value := range sortedValues(own) {
		if !constantValues[value] {
			t.Errorf("the policy module declares %q, which is no permission constant", value)
		}
	}

	// A module may declare a permission of its own beside them, and its row.
	reg.Register(businessModule())
	union := catalogOf(t, reg)
	if !union[string(businessPermission)] {
		t.Errorf("the catalog does not carry the module's declared permission %q", businessPermission)
	}
	for _, value := range sortedValues(union) {
		if !translated(reg, authz.Permission(value)) {
			t.Errorf("the catalog declares %q, which has no translation row", value)
		}
	}
}

// businessPermission is a permission a module declares of its own, beside the
// ones this capability carries, and one no permission constant names.
const businessPermission = authz.Permission("crm.deal.read")

// businessModule declares businessPermission: the permission spec and the
// translation row beside it, the way a business module carries both.
func businessModule() core.Module {
	return core.Module{
		Name: "stand-in.business",
		Resources: []any{
			authz.PermissionSpec{Permissions: []authz.Permission{businessPermission}},
			authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
				businessPermission: {Object: "deal", Action: "read"},
			}},
		},
	}
}

// catalogOf collects every permission the registry's specs declare.
func catalogOf(_ *testing.T, reg *core.Registry) map[string]bool {
	declared := map[string]bool{}
	for _, resource := range core.Resources[authz.PermissionSpec](reg) {
		for _, p := range resource.Value.Permissions {
			declared[string(p)] = true
		}
	}
	return declared
}

// translated reports whether a declared permission has a translation row: a
// built-in row for a permission constant, or a registered module's
// TranslationSpec row for a permission of its own. It is the two sources the
// leaves union into the process's translation.
//
// The built-in half is read through Builtin and never through Lookup, which
// answers from whatever set the process last resolved: a case in this binary
// that drove an assembly would otherwise make a permission nothing declares a
// row for look translated, and this guard would pass over the gap it exists to
// find.
func translated(reg *core.Registry, permission authz.Permission) bool {
	if slices.Contains(translation.Builtin(), permission) {
		return true
	}
	for _, resource := range core.Resources[authz.TranslationSpec](reg) {
		if _, ok := resource.Value.Rows[permission]; ok {
			return true
		}
	}
	return false
}

// sortedValues gives a set's members in order, so a failure message and a
// counted comparison do not depend on map iteration order.
func sortedValues(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// The vocabulary reading is held to be one: it finds every constant of the
// named type whatever the shape of its declaration — an explicit type, a
// conversion, or a value the compiler folds from a longer expression — and
// ignores a constant of another type or an untyped one, so a vocabulary guard
// built on it is not a check that would read nothing and not one that would miss
// a constant spelled the long way.
func TestTheVocabularyReadSeesWhatItIsMeantToSee(t *testing.T) {
	dir := t.TempDir()
	body := `package sample

type Action string

const (
	ActionAlpha    Action = "alpha"
	ActionBeta            = Action("beta")
	ActionComputed Action = "comp" + "uted"
	NotAnAction    string = "gamma"
	Untyped               = "delta"
)
`
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the sample package: %v", err)
	}
	got := declaredConsts(t, dir, "Action")
	want := map[string]string{
		"ActionAlpha":    "alpha",
		"ActionBeta":     "beta",
		"ActionComputed": "computed",
	}
	if len(got) != len(want) {
		t.Fatalf("the vocabulary read found %v, want exactly %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("the vocabulary read has %s = %q, want %q", name, got[name], value)
		}
	}
}
