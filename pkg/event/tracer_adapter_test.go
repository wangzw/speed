package event_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"testing"
)

var implementationSpecFiles = []string{
	filepath.Join("memory", "memory.go"),
	filepath.Join("redis", "redis.go"),
	filepath.Join("postgres", "postgres.go"),
	filepath.Join("nats", "nats.go"),
}

// TestEveryImplementationAdaptsTheProviderThroughTheSharedUnit holds the four
// implementations to the shared adaptation unit.
func TestEveryImplementationAdaptsTheProviderThroughTheSharedUnit(t *testing.T) {
	for _, path := range implementationSpecFiles {
		t.Run(filepath.Dir(path), func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			var seen bool
			ast.Inspect(file, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok || !isEventSpec(cl.Type) {
					return true
				}
				for _, el := range cl.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "Trace" {
						continue
					}
					seen = true
					if !isSharedProvider(kv.Value) {
						t.Errorf("%s: the Spec's Trace is %s, want tracing.Provider: the code "+
							"adapting the provider to this package's narrow face has to be the one "+
							"shared unit every implementation takes up, or the copies drift apart",
							path, exprString(fset, kv.Value))
					}
				}
				return true
			})
			if !seen {
				t.Errorf("%s: no event.Spec carrying a Trace field was found, so nothing here pins "+
					"where this implementation adapts the telemetry provider", path)
			}
		})
	}
}

func isEventSpec(t ast.Expr) bool {
	idx, ok := t.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "event" && sel.Sel.Name == "Spec"
}

func isSharedProvider(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "tracing" && sel.Sel.Name == "Provider"
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, e); err != nil {
		return "<unrenderable expression>"
	}
	return buf.String()
}
