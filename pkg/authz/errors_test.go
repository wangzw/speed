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

// TestTheSentinelSetIsClosed checks that the sentinel set is closed.
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

func thisPackageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test file")
	}
	return filepath.Dir(file)
}
