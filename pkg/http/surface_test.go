package http

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
)

// exportedSurface is the package's exported surface, written out. Every entry
// is one declaration a dependant can see and therefore one this module has to
// keep.
//
// The list is here rather than derived because deriving it would only restate
// whatever the code happens to say. Pinned, a new exported symbol has to be
// added here by hand, and the entry is where the reader asks whether it
// belongs on the surface at all. A knob that changes how a request body is
// decoded is exactly that kind of addition: body binding is gin's, and an
// exported option for it would fail this test the moment it appeared.
var exportedSurface = []string{
	"const ModuleName",
	"const OrderInnermost",
	"const OrderOutermost",
	"const StageAuthorize",
	"const StageGuard",
	"const StageIdentify",
	"const StageObserve",
	"const StageRecord",
	"const StageThrottle",
	"field Middleware.After",
	"field Middleware.Before",
	"field Middleware.Handler",
	"field Middleware.Name",
	"field Middleware.Order",
	"field Middleware.Provides",
	"field Middleware.Stage",
	"field Problem.Cause",
	"field Problem.Detail",
	"field Problem.Instance",
	"field Problem.Status",
	"field Problem.Title",
	"field Problem.Type",
	"field Spec.Document",
	"field Spec.Endpoint",
	"func Draining",
	"func Fail",
	"func Module",
	"method Endpoint.Accepting",
	"method Endpoint.Route",
	"method Endpoint.Use",
	"method ErrorResponder.RespondError",
	"method Router.Endpoint",
	"method Router.Endpoints",
	"type Endpoint",
	"type ErrorResponder",
	"type Middleware",
	"type Problem",
	"type Router",
	"type Spec",
	"type Stage",
	"var ErrDrainTimeout",
	"var ErrInvalidSpec",
	"var ErrListen",
	"var ErrMiddlewareCycle",
	"var ErrRouteConflict",
	"var ErrStageConflict",
	"var ErrUnknownEndpoint",
}

// The registration surface is expressed in gin's types: a route takes
// gin.HandlerFuncs, a layer is one, and the error entry point takes the gin
// context. The names above would stay put through a change of anchor; these
// declarations stop compiling.
var (
	_ func(Endpoint, string, string, ...gin.HandlerFunc) = Endpoint.Route
	_ func(*gin.Context, Problem)                        = Fail
	_ gin.HandlerFunc                                    = Middleware{}.Handler
)

// TestExportedSurfaceIsPinned compares the declarations the source really
// exports with the list above, in both directions: a symbol added to the code
// and not to the list fails, and so does one removed from the code while the
// list still names it.
func TestExportedSurfaceIsPinned(t *testing.T) {
	_, files := parseProductionFiles(t)
	got := collectExported(files)
	want := slices.Clone(exportedSurface)
	slices.Sort(want)
	slices.Sort(got)

	for _, entry := range got {
		if !slices.Contains(want, entry) {
			t.Errorf("the source exports %q, which the pinned surface does not list. "+
				"Add it to exportedSurface only after deciding it belongs on the surface", entry)
		}
	}
	for _, entry := range want {
		if !slices.Contains(got, entry) {
			t.Errorf("the pinned surface lists %q, which the source no longer exports. "+
				"Removing an entry is a breaking change for every dependant", entry)
		}
	}
}

// collectExported renders every exported top-level declaration, and the
// exported members of exported types, as one entry each.
func collectExported(files []*ast.File) []string {
	var out []string
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					out = append(out, "func "+d.Name.Name)
					continue
				}
				if recv := receiverName(d.Recv); recv != "" {
					out = append(out, "method "+recv+"."+d.Name.Name)
				}
			case *ast.GenDecl:
				out = append(out, exportedFromGenDecl(d)...)
			}
		}
	}
	return out
}

// receiverName renders the receiver's type name, empty when the receiver type
// is unexported and the method is therefore not on the surface.
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if index, ok := expr.(*ast.IndexExpr); ok {
		expr = index.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok || !ident.IsExported() {
		return ""
	}
	return ident.Name
}

func exportedFromGenDecl(d *ast.GenDecl) []string {
	var out []string
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			kind := "var"
			if d.Tok == token.CONST {
				kind = "const"
			}
			for _, name := range s.Names {
				if name.IsExported() {
					out = append(out, kind+" "+name.Name)
				}
			}
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, "type "+s.Name.Name)
			out = append(out, exportedMembers(s)...)
		}
	}
	return out
}

func exportedMembers(s *ast.TypeSpec) []string {
	var out []string
	switch underlying := s.Type.(type) {
	case *ast.StructType:
		for _, field := range underlying.Fields.List {
			for _, name := range field.Names {
				if name.IsExported() {
					out = append(out, "field "+s.Name.Name+"."+name.Name)
				}
			}
		}
	case *ast.InterfaceType:
		for _, method := range underlying.Methods.List {
			if len(method.Names) == 0 {
				out = append(out, "embed "+s.Name.Name+"."+types.ExprString(method.Type))
				continue
			}
			for _, name := range method.Names {
				if name.IsExported() {
					out = append(out, "method "+s.Name.Name+"."+name.Name)
				}
			}
		}
	}
	return out
}
