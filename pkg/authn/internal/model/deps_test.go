package model_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modelPath = "github.com/vislake/speed/pkg/authn/internal/model"

// TestTheValueTypePackagesDependOnLittle holds the layering the packaging
// decision rests on: model imports only the standard library, and identity,
// verdict and step import only the standard library and model, and none of the
// three imports another. A package outside the standard library in any of them
// would enter the closure of every module that imports the root package. The
// root package's own guard comes with the root package.
func TestTheValueTypePackagesDependOnLittle(t *testing.T) {
	const base = "github.com/vislake/speed/pkg/authn/internal/"
	for _, c := range []struct {
		pkg   string
		allow []string
	}{
		{base + "model", nil},
		{base + "identity", []string{modelPath}},
		{base + "verdict", []string{modelPath}},
		{base + "step", []string{modelPath}},
	} {
		t.Run(strings.TrimPrefix(c.pkg, base), func(t *testing.T) {
			for _, imp := range importsOf(t, c.pkg) {
				if isStandard(imp) || slices.Contains(c.allow, imp) {
					continue
				}
				t.Errorf("%s imports %s; it may import only the standard library and %v", c.pkg, imp, c.allow)
			}
		})
	}
}

// importsOf lists the imports of every non-test Go file in the package's
// directory. The files are parsed and not asked of go list, which evaluates
// build constraints and so would not show a file built only under another tag
// or on another platform; a third-party import in such a file enters the
// closure all the same.
func importsOf(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", pkg, err, out)
	}
	dir := strings.TrimSpace(string(out))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var imports []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: import %s: %v", name, spec.Path.Value, err)
			}
			imports = append(imports, path)
		}
	}
	return imports
}

// isStandard reports whether an import path is the standard library's, which is
// the paths whose first element has no dot.
func isStandard(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
