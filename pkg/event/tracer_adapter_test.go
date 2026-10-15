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

// implementationSpecFiles are the four implementations' source files, named
// relative to this package's directory. Each holds its implementation's
// event.Spec, which is where the adapter that satisfies Spec.Trace is chosen.
var implementationSpecFiles = []string{
	filepath.Join("memory", "memory.go"),
	filepath.Join("redis", "redis.go"),
	filepath.Join("postgres", "postgres.go"),
	filepath.Join("nats", "nats.go"),
}

// TestEveryImplementationAdaptsTheProviderThroughTheSharedUnit holds the four
// implementations to the design's rule that the code adapting the telemetry
// provider to this package's narrow TracerProvider lives in one shared unit and
// every implementation takes it up: a copy per backend drifts, and the seam it
// carries — the span the dispatch layer records, the propagator it reads the
// event's context through — would then differ by backend.
//
// The clause is not observable through behaviour: a private adapter that behaves
// exactly like the shared one produces the same spans, the same parents and the
// same attributes, so no case that drives a delivery can tell the two apart.
// What separates them is identity — which function the Spec's Trace names — so
// the assertion is structural: it reads each implementation's source and holds
// the Trace field of its event.Spec to the shared unit's provider function. A
// private adapter of another name fails it however faithfully it behaves.
//
// It parses the source rather than reflecting over a value because the Spec an
// implementation fills in is not carried on the descriptor event.NewModule
// returns — the constructor closure closes over it — so the Trace function is
// not reachable from a built module at run time.
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

// isEventSpec reports whether t names the root package's Spec type, written as
// event.Spec[Config].
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

// isSharedProvider reports whether t names the shared unit's provider function,
// written as tracing.Provider.
func isSharedProvider(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "tracing" && sel.Sel.Name == "Provider"
}

// exprString renders an expression as source, so a failure names what the Trace
// field actually holds.
func exprString(fset *token.FileSet, e ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, e); err != nil {
		return "<unrenderable expression>"
	}
	return buf.String()
}
