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

// goListBudget bounds the one go list this file runs, which lists the export
// files of this package's dependency closure. With a warm build cache it returns
// in about a second; with a cold one it compiles that closure first, which can
// take much longer. The budget leaves room for a cold cache and still catches a
// return to one go list per import, which took minutes.
const goListBudget = 2 * time.Minute

// checkBudget bounds the whole export-surface check end to end. Type-checking
// this package's files against the listed export data takes about a second; the
// budget catches a return to resolving imports by type-checking them from
// source, which forked one go list per import and took minutes. That path would
// not run inside the single go list above, so it needs its own bound.
const checkBudget = 5 * time.Minute

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
// the symbols it exports. Imports are resolved from the go list export data
// through the gc importer, not from their source: the source importer forked one
// go list per import, which made this check take minutes.
func checkedPackage(t *testing.T) (*types.Package, []exportedType) {
	t.Helper()
	type result struct {
		checked packageCheck
		err     error
	}
	// The check runs off the test goroutine so that checkBudget can bound it: a
	// regression to type-checking imports from source would otherwise hang until
	// the go test timeout, past the point of saying which check regressed.
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

// packageCheck is one type check's result.
type packageCheck struct {
	pkg      *types.Package
	exported []exportedType
}

// checkPackage is computed once, so the two cases that read it type-check this
// package once rather than twice.
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

// exportData lists this package's dependency closure once: every import path
// the compiler knows about, mapped to its compiled export file. The gc importer
// reads those files instead of type-checking each dependency from source.
var exportData = sync.OnceValues(listExportData)

// listExportData runs the one go list that feeds the gc importer. It inherits
// this process's environment variables, so it reads the same GOFLAGS and the
// same build cache location. Build tags passed on the go test command line are
// not inherited this way; only tags written into GOFLAGS reach the subprocess.
// This package's non-test files carry no build tags, so no tag needs to be
// forwarded to the go list arguments.
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

// gcImporter is shared by this package's check and by the library-reference
// check, so both read the same export data instead of starting another importer.
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
