package authz

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
)

// TestTheSentinelSetIsClosed pins the root package's sentinels to the closed set
// the design's failure table names. The table is the one list a caller judges a
// failure against with errors.Is, so a sentinel added, dropped or renamed is a
// change to a published surface and is a decision, not an addition — every
// direction fails here.
//
// Each sentinel is taken up by name in the table below, so a rename or a removal
// breaks the build rather than passing on the ones that remain; the set the
// sources declare is then read back and held to exactly these names, so a
// sentinel that joins the package without joining the table fails too. Two
// sentinels that publish one message would be one failure a caller cannot tell
// from another, so the messages are held distinct.
func TestTheSentinelSetIsClosed(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrNoRouter", ErrNoRouter},
		{"ErrUnknownEndpoint", ErrUnknownEndpoint},
		{"ErrUnknownPermission", ErrUnknownPermission},
		{"ErrPlatformDomainMissing", ErrPlatformDomainMissing},
		{"ErrReservedRole", ErrReservedRole},
		{"ErrLastTenantAdmin", ErrLastTenantAdmin},
		{"ErrMissingMembershipChecker", ErrMissingMembershipChecker},
		{"ErrPlatformMembershipRequired", ErrPlatformMembershipRequired},
		{"ErrUnknownRole", ErrUnknownRole},
		{"ErrBuiltinRole", ErrBuiltinRole},
		{"ErrTenantMismatch", ErrTenantMismatch},
		{"ErrUndeclaredRoute", ErrUndeclaredRoute},
		{"ErrPolicyUnavailable", ErrPolicyUnavailable},
	}

	want := make([]string, 0, len(sentinels))
	seen := map[string]string{}
	for _, s := range sentinels {
		want = append(want, s.name)
		if s.err == nil {
			t.Errorf("%s is nil, want a sentinel error value", s.name)
			continue
		}
		if other, dup := seen[s.err.Error()]; dup {
			t.Errorf("%s and %s publish one message %q, so a caller cannot tell them apart", s.name, other, s.err.Error())
		}
		seen[s.err.Error()] = s.name
	}

	declared := rootSentinelDecls(t)
	if len(declared) == 0 {
		t.Fatal("the root sources declare no errors.New sentinel, so the reading found nothing")
	}
	got := make([]string, 0, len(declared))
	for name := range declared {
		got = append(got, name)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the root package declares the sentinels %q, want exactly %q", got, want)
	}
}

// rootSentinelDecls reads the root package's non-test files and returns every
// package-level variable declared from a call to errors.New, keyed by name; the
// value is the message the call is given. A sentinel is a stable errors.New
// value a caller judges with errors.Is, so that constructor is the shape read: a
// var of another shape is not a sentinel and is not part of the set.
func rootSentinelDecls(t *testing.T) map[string]string {
	t.Helper()
	dir := thisPackageDir(t)
	pkg, err := build.Default.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	found := map[string]string{}
	for _, name := range pkg.GoFiles {
		file, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		collectSentinels(t, file, found)
	}
	return found
}

// collectSentinels adds to into the package-level variables of file declared
// from a call to errors.New, keyed by name. A var whose value is not such a call
// is skipped.
func collectSentinels(t *testing.T, file *ast.File, into map[string]string) {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, nameIdent := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				message, ok := errorsNewMessage(vs.Values[i])
				if !ok {
					continue
				}
				into[nameIdent.Name] = message
			}
		}
	}
}

// errorsNewMessage reports the string errors.New is given when expr is a call to
// errors.New, and whether expr is such a call.
func errorsNewMessage(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" {
		return "", false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != "errors" {
		return "", false
	}
	if len(call.Args) != 1 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	message, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return message, true
}

// thisPackageDir is this test file's directory, the root package pkg/authz. It is
// read from the file's own location so that the reading finds the sources
// wherever go test is run from.
func thisPackageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file")
	}
	return filepath.Dir(file)
}
