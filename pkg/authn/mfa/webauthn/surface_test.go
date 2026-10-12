package webauthn

import (
	"go/ast"
	gobuild "go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// libraryPath is the import path of the WebAuthn library whose types must not
// reach this package's export surface. It is at version 0.x and changes between
// releases, so a caller tied to one of its types is tied to the version this
// module pins.
const libraryPath = "github.com/go-webauthn/webauthn"

// exportedType is one exported symbol's type, held for the walk.
type exportedType struct {
	name string
	typ  types.Type
}

// checkedPackage type-checks this package from source and returns its scope and
// the symbols it exports.
func checkedPackage(t *testing.T) (*types.Package, []exportedType) {
	t.Helper()
	bp, err := gobuild.Default.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("reading this directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range bp.GoFiles {
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", name, perr)
		}
		files = append(files, f)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check(bp.Name, fset, files, nil)
	if err != nil {
		t.Fatalf("type-checking this package: %v", err)
	}
	var out []exportedType
	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		if !obj.Exported() {
			continue
		}
		if tn, ok := obj.(*types.TypeName); ok {
			out = append(out, exportedType{name: name, typ: types.Unalias(tn.Type())})
			continue
		}
		out = append(out, exportedType{name: name, typ: obj.Type()})
	}
	return pkg, out
}

// libraryRefs reports the library types reachable from typ, following named
// types, signatures, structs, interfaces and containers.
func libraryRefs(typ types.Type, seen map[types.Type]bool) []string {
	if typ == nil || seen[typ] {
		return nil
	}
	seen[typ] = true
	switch t := typ.(type) {
	case *types.Named:
		var out []string
		if obj := t.Obj(); obj != nil && obj.Pkg() != nil && strings.HasPrefix(obj.Pkg().Path(), libraryPath) {
			out = append(out, obj.Pkg().Path()+"."+obj.Name())
		}
		out = append(out, libraryRefs(t.Underlying(), seen)...)
		for i := 0; i < t.NumMethods(); i++ {
			out = append(out, libraryRefs(t.Method(i).Type(), seen)...)
		}
		return out
	case *types.Pointer:
		return libraryRefs(t.Elem(), seen)
	case *types.Slice:
		return libraryRefs(t.Elem(), seen)
	case *types.Array:
		return libraryRefs(t.Elem(), seen)
	case *types.Map:
		return append(libraryRefs(t.Key(), seen), libraryRefs(t.Elem(), seen)...)
	case *types.Chan:
		return libraryRefs(t.Elem(), seen)
	case *types.Signature:
		out := libraryRefs(t.Params(), seen)
		out = append(out, libraryRefs(t.Results(), seen)...)
		return out
	case *types.Tuple:
		var out []string
		for i := 0; i < t.Len(); i++ {
			out = append(out, libraryRefs(t.At(i).Type(), seen)...)
		}
		return out
	case *types.Struct:
		var out []string
		for i := 0; i < t.NumFields(); i++ {
			out = append(out, libraryRefs(t.Field(i).Type(), seen)...)
		}
		return out
	case *types.Interface:
		var out []string
		for i := 0; i < t.NumMethods(); i++ {
			out = append(out, libraryRefs(t.Method(i).Type(), seen)...)
		}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			out = append(out, libraryRefs(t.EmbeddedType(i), seen)...)
		}
		return out
	}
	return nil
}

// The export surface carries no type of the WebAuthn library: a caller is tied
// to the root package's Factor, Step and LoginMethod, and the version this module
// pins reaches no further than its own internals.
func TestNoLibraryTypeReachesTheExportSurface(t *testing.T) {
	_, exported := checkedPackage(t)
	for _, e := range exported {
		if refs := libraryRefs(e.typ, map[types.Type]bool{}); len(refs) > 0 {
			t.Errorf("the exported symbol %q reaches the library types %v", e.name, refs)
		}
	}
}

// The export surface is small and pinned: the module name, the config namespace
// and the descriptor. A symbol added without a decision fails here.
func TestTheExportSurfaceIsPinned(t *testing.T) {
	_, exported := checkedPackage(t)
	var got []string
	for _, e := range exported {
		got = append(got, e.name)
	}
	slices.Sort(got)
	want := []string{"ConfigNamespace", "Module", "ModuleName"}
	if !slices.Equal(got, want) {
		t.Errorf("the export surface is %v, want %v", got, want)
	}
}

// The check sees what it is meant to see: a type the library defines is
// reachable from a symbol that names it, which is the defect the test above
// refuses.
func TestTheLibraryRefCheckSeesTheLibrary(t *testing.T) {
	libPkg, err := importer.ForCompiler(token.NewFileSet(), "source", nil).Import(libraryPath + "/webauthn")
	if err != nil {
		t.Fatalf("importing the library: %v", err)
	}
	obj := libPkg.Scope().Lookup("Credential")
	if obj == nil {
		t.Fatal("the library exports no Credential")
	}
	if refs := libraryRefs(obj.Type(), map[types.Type]bool{}); len(refs) == 0 {
		t.Error("the walk found no library type in a library type")
	}
}
