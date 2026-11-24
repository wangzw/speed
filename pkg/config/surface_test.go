package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoExportedFunctionHandsOutAnUnexportedType checks that no exported function hands out an unexported type.
func TestNoExportedFunctionHandsOutAnUnexportedType(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package's sources failed: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s failed: %v", name, err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parsing %s failed: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			checked++
			for _, result := range fn.Type.Results.List {
				if hidden := unexportedLocalType(result.Type); hidden != "" {
					t.Errorf("%s: %s returns %s, a type this package does not export",
						fset.Position(fn.Pos()), fn.Name.Name, hidden)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no exported function with a result was found, so nothing was checked")
	}
}

func unexportedLocalType(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return unexportedLocalType(e.X)
	case *ast.Ident:
		if e.IsExported() || types.Universe.Lookup(e.Name) != nil {
			return ""
		}
		return e.Name
	}
	return ""
}
