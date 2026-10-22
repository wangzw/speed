package twilio

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoVendorTypeReachesTheExportedSurface holds that the vendor SDK is an
// implementation detail: the package exports the module name, its config
// namespace and the module descriptor and nothing else, and no exported
// signature names a vendor type, so a host that imports this package to register
// it never has to name the SDK.
func TestNoVendorTypeReachesTheExportedSurface(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this package's directory: %v", err)
	}

	var exported []string
	parsed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed++
		vendorAliases := vendorImportNames(file)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// A method on an unexported type is not on the package's surface.
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				exported = append(exported, "func "+d.Name.Name)
				if namesVendorType(d.Type, vendorAliases) {
					t.Errorf("the signature of %s names a vendor type", d.Name.Name)
				}
			case *ast.GenDecl:
				exported = append(exported, exportedSpec(d.Tok, d.Specs)...)
			}
		}
	}
	if parsed == 0 {
		t.Fatal("no non-test source file was parsed, so the surface was not read")
	}

	want := []string{"const ModuleName", "const ConfigNamespace", "func Module"}
	if len(exported) != len(want) {
		t.Fatalf("exported surface = %v, want %v", exported, want)
	}
	for _, w := range want {
		if !containsString(exported, w) {
			t.Errorf("exported surface = %v, want it to hold %q", exported, w)
		}
	}
}

// vendorImportNames returns the local names a file gives the vendor packages.
func vendorImportNames(file *ast.File) map[string]bool {
	names := make(map[string]bool)
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.Contains(path, "twilio-go") {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			parts := strings.Split(path, "/")
			name = parts[len(parts)-1]
		}
		names[name] = true
	}
	return names
}

// namesVendorType reports whether a type expression is a selector on a vendor
// package.
func namesVendorType(node ast.Node, aliases map[string]bool) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && aliases[ident.Name] {
			found = true
		}
		return true
	})
	return found
}

// exportedSpec names the exported declarations a spec introduces, under the
// keyword of the declaration it belongs to.
func exportedSpec(tok token.Token, specs []ast.Spec) []string {
	var names []string
	for _, spec := range specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			kind := "var"
			if tok == token.CONST {
				kind = "const"
			}
			for _, name := range s.Names {
				if name.IsExported() {
					names = append(names, kind+" "+name.Name)
				}
			}
		case *ast.TypeSpec:
			if s.Name.IsExported() {
				names = append(names, "type "+s.Name.Name)
			}
		}
	}
	return names
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
