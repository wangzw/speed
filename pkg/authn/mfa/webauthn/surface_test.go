package webauthn

import (
	"context"
	"fmt"
	"go/ast"
	gobuild "go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const goListBudget = 2 * time.Minute

const checkBudget = 5 * time.Minute

const libraryPath = "github.com/go-webauthn/webauthn"

type exportedType struct {
	name string
	typ  types.Type
}

func checkedPackage(t *testing.T) (*types.Package, []exportedType) {
	t.Helper()
	type result struct {
		checked packageCheck
		err     error
	}
	done := make(chan result, 1)
	go func() {
		checked, err := checkPackage()
		done <- result{checked, err}
	}()
	timer := time.NewTimer(checkBudget)
	defer timer.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.checked.pkg, r.checked.exported
	case <-timer.C:
		t.Fatalf("the export-surface check did not finish within %s; it takes about a second, so it is probably type-checking imports from source again", checkBudget)
		return nil, nil
	}
}

type packageCheck struct {
	pkg      *types.Package
	exported []exportedType
}

var checkPackage = sync.OnceValues(func() (packageCheck, error) {
	bp, err := gobuild.Default.ImportDir(".", 0)
	if err != nil {
		return packageCheck{}, fmt.Errorf("reading this directory: %w", err)
	}
	imp, err := gcImporter()
	if err != nil {
		return packageCheck{}, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range bp.GoFiles {
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if perr != nil {
			return packageCheck{}, fmt.Errorf("parsing %s: %w", name, perr)
		}
		files = append(files, f)
	}
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check(bp.Name, fset, files, nil)
	if err != nil {
		return packageCheck{}, fmt.Errorf("type-checking this package: %w", err)
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
	return packageCheck{pkg: pkg, exported: out}, nil
})

var exportData = sync.OnceValues(listExportData)

func listExportData() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goListBudget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-export", "-f", "{{.ImportPath}}\t{{.Export}}", ".")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("a single go list did not finish within %s; this can be a cold build cache compiling the dependency closure, or a return to one go list per import", goListBudget)
		}
		return nil, fmt.Errorf("running go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	data := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		path, file, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("go list printed %q, which is neither an import path nor an export file", line)
		}
		data[path] = file
	}
	return data, nil
}

var gcImporter = sync.OnceValues(func() (types.Importer, error) {
	data, err := exportData()
	if err != nil {
		return nil, err
	}
	return importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		file, ok := data[path]
		if !ok {
			return nil, fmt.Errorf("go list reported no export data for the import %q; the check cannot see it and must not skip it", path)
		}
		if file == "" {
			return nil, fmt.Errorf("go list reported no export file for the import %q; the check cannot see it and must not skip it", path)
		}
		f, err := os.Open(file)
		if err != nil {
			return nil, fmt.Errorf("opening the export file for %q: %w", path, err)
		}
		return f, nil
	}), nil
})

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

// TestNoLibraryTypeReachesTheExportSurface checks that no WebAuthn library type reaches the export surface.
func TestNoLibraryTypeReachesTheExportSurface(t *testing.T) {
	_, exported := checkedPackage(t)
	for _, e := range exported {
		if refs := libraryRefs(e.typ, map[types.Type]bool{}); len(refs) > 0 {
			t.Errorf("the exported symbol %q reaches the library types %v", e.name, refs)
		}
	}
}

// TestTheExportSurfaceIsPinned checks that the export surface is exactly the module name, config namespace and descriptor.
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

// TestTheLibraryRefCheckSeesTheLibrary checks that the library-reference check finds a library type.
func TestTheLibraryRefCheckSeesTheLibrary(t *testing.T) {
	imp, err := gcImporter()
	if err != nil {
		t.Fatal(err)
	}
	libPkg, err := imp.Import(libraryPath + "/webauthn")
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
