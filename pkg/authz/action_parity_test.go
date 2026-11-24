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

func thisDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file")
	}
	return filepath.Dir(file)
}

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

func isNamedType(typ types.Type, typeName string) bool {
	named, ok := types.Unalias(typ).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Name() == typeName
}

func actionVocabulary(t *testing.T) map[string]string {
	t.Helper()
	return declaredConsts(t, filepath.Join(thisDir(t), "..", "authn"), "Action")
}

func permissionVocabulary(t *testing.T) map[string]string {
	t.Helper()
	return declaredConsts(t, thisDir(t), "Permission")
}

func sortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestEveryActionHasAMatchingPermission checks that every action has a matching permission.
func TestEveryActionHasAMatchingPermission(t *testing.T) {
	actions := actionVocabulary(t)
	permissions := permissionVocabulary(t)

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

// TestTheTranslationCoversEveryPermission checks that the translation covers every permission.
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

// TestTheCatalogDeclaresEveryPermission checks that the catalog declares every permission.
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

const businessPermission = authz.Permission("crm.deal.read")

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

func catalogOf(_ *testing.T, reg *core.Registry) map[string]bool {
	declared := map[string]bool{}
	for _, resource := range core.Resources[authz.PermissionSpec](reg) {
		for _, p := range resource.Value.Permissions {
			declared[string(p)] = true
		}
	}
	return declared
}

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

func sortedValues(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// TestTheVocabularyReadSeesWhatItIsMeantToSee checks that the vocabulary read sees what it is meant to see.
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
