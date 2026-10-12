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
	"sort"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
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
// The guard runs from package authz_test because it imports the guard
// subpackage, which imports the root package: an internal test of the root could
// not import it without a cycle.

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
		if object, action, ok := guard.ObjectAction(authz.Permission(value)); !ok || object == "" || action == "" {
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
		object, action, ok := guard.ObjectAction(authz.Permission(value))
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

// The policy module's catalog declares every permission constant, and declares
// nothing that is not one: a constant the catalog omits is one no route may
// reference, and a declaration naming no constant is a permission the vocabulary
// does not hold.
//
// The catalog's declaration is read from the module the way its own build reads
// it, rather than by driving the module: the module's construction needs a
// database, and what is under guard here is the vocabulary and not the store.
func TestTheCatalogDeclaresEveryPermission(t *testing.T) {
	permissions := permissionVocabulary(t)

	reg := core.New()
	reg.Register(policy.Module())
	specs := core.Resources[authz.PermissionSpec](reg)
	if len(specs) == 0 {
		t.Fatal("the policy module declares no permission spec, so the catalog holds nothing to compare")
	}
	declared := map[string]bool{}
	for _, resource := range specs {
		for _, p := range resource.Value.Permissions {
			declared[string(p)] = true
		}
	}

	constantNames := make(map[string]string, len(permissions))
	for name, value := range permissions {
		constantNames[value] = name
	}
	for _, name := range sortedNames(permissions) {
		value := permissions[name]
		if !declared[value] {
			t.Errorf("the catalog does not declare the permission %s (%q)", name, value)
		}
	}
	values := make([]string, 0, len(declared))
	for value := range declared {
		values = append(values, value)
	}
	sort.Strings(values)
	for _, value := range values {
		if _, ok := constantNames[value]; !ok {
			t.Errorf("the catalog declares %q, which is no permission constant", value)
		}
	}
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
