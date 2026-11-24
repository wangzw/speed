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

// TestTheValueTypePackagesDependOnLittle holds that model imports only the
// standard library and identity, verdict and step import only those and model.
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
// directory, parsed rather than asked of go list so a file built only under
// another tag still counts.
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

func isStandard(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
