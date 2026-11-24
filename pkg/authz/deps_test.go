package authz

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The import paths the rules below are about.
const (
	moduleImportPath = "github.com/vislake/speed/pkg"
	rootPath         = moduleImportPath + "/authz"
	authztestPath    = rootPath + "/authztest"
)

// listed is what these checks read about one package: its import path and the
// imports of its non-test files. go list reports the test files' imports
// separately, and the rules are about production code, so those are not asked
// for.
type listed struct {
	ImportPath string
	// Imports are the imports of the package's non-test files that build here.
	Imports []string
}

// goList lists every package of the module with go list and returns them. The
// pattern names the module, so the listing does not depend on the directory the
// tests run from. -e keeps a package that does not compile in the listing, with
// its imports as the parser read them.
func goList(t *testing.T) []listed {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Imports", moduleImportPath+"/...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s/...: %v\n%s", moduleImportPath, err, stderr.String())
	}
	var pkgs []listed
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				return pkgs
			}
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
}

// goListDeps returns the import paths of path and its non-test dependency
// closure, in go list's order.
func goListDeps(t *testing.T, path string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", path, err, stderr.String())
	}
	return strings.Fields(string(out))
}

func find(pkgs []listed, path string) (listed, bool) {
	for _, p := range pkgs {
		if p.ImportPath == path {
			return p, true
		}
	}
	return listed{}, false
}

// isStandard reports whether an import path is the standard library's, which is
// the paths whose first element has no dot.
func isStandard(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// rootImportViolations is the first rule: the root package's non-test imports
// are the standard library and nothing else. The root package is imported by
// every consumer of a permission, so a third-party library reached from here
// would enter all of their closures; the Casbin engine and its storage live in
// the policy subpackage for that reason.
func rootImportViolations(root listed) []string {
	var out []string
	for _, imp := range root.Imports {
		if isStandard(imp) {
			continue
		}
		out = append(out, rootPath+" imports "+imp+": the root package may import the standard library only")
	}
	slices.Sort(out)
	return out
}

// closureViolations is the second rule, read from the root package's whole
// non-test closure rather than its direct imports: the closure holds no Casbin,
// gin or gorm, and no subpackage of this directory. The direct-import rule
// already keeps the closure to the standard library; this holds the named
// libraries and the layer boundary even if the first rule is ever loosened.
func closureViolations(deps []string) []string {
	var out []string
	for _, imp := range deps {
		switch {
		case imp == "github.com/gin-gonic/gin" || strings.HasPrefix(imp, "github.com/gin-gonic/gin/"),
			strings.HasPrefix(imp, "github.com/casbin/"),
			strings.HasPrefix(imp, "gorm.io/"):
			out = append(out, rootPath+"'s closure holds "+imp+
				": the root package's closure is the standard library")
		case strings.HasPrefix(imp, rootPath+"/"):
			out = append(out, rootPath+"'s closure holds "+imp+
				": the root package imports none of this directory's subpackages")
		}
	}
	slices.Sort(out)
	return out
}

// authztestViolations is the third rule: no package imports authztest in a file
// that is not a test, except authztest itself. The compiler cannot stop
// production code importing it, and the fixtures it hands out are for tests;
// importing it from production code would put a test builder into a shipped
// binary. An import from a test file is not reported: go list hands the
// non-test imports alone, and that is exactly the split the rule is about.
func authztestViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		if p.ImportPath == authztestPath {
			continue
		}
		if slices.Contains(p.Imports, authztestPath) {
			out = append(out, p.ImportPath+" imports authztest outside a test file")
		}
	}
	slices.Sort(out)
	return out
}

// TestTheRootPackageImportsOnlyTheStandardLibrary checks that the root package
// and its closure import only the standard library.
func TestTheRootPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	pkgs := goList(t)

	root, ok := find(pkgs, rootPath)
	if !ok {
		t.Fatalf("the listing does not hold %s, so what the rules read is not this module", rootPath)
	}
	if len(root.Imports) == 0 {
		t.Fatal("the root package lists no import, so the listing is not reading its files")
	}
	if !slices.Contains(root.Imports, "context") {
		t.Fatalf("the root package does not list the context import, so the listing is not reading imports: %v", root.Imports)
	}

	deps := goListDeps(t, rootPath)
	if !slices.Contains(deps, rootPath) {
		t.Fatalf("the closure does not hold %s, so it is not the root package's", rootPath)
	}
	if !slices.Contains(deps, "errors") {
		t.Fatalf("the closure does not hold a standard library package, so the listing is not reading imports: %v", deps)
	}

	for _, v := range rootImportViolations(root) {
		t.Error(v)
	}
	for _, v := range closureViolations(deps) {
		t.Error(v)
	}
}

// TestNoProductionCodeImportsAuthztest checks that authztest is imported from
// tests alone across every package of the module.
func TestNoProductionCodeImportsAuthztest(t *testing.T) {
	pkgs := goList(t)
	if _, ok := find(pkgs, authztestPath); !ok {
		t.Fatalf("the listing does not hold %s, so the fixture rule has nothing to read", authztestPath)
	}
	for _, v := range authztestViolations(pkgs) {
		t.Error(v)
	}
}

// TestTheRulesFailOnAViolation checks that each import rule reports a violating
// import and accepts a legal one.
func TestTheRulesFailOnAViolation(t *testing.T) {
	root := listed{ImportPath: rootPath, Imports: []string{"context", "time", "example.com/lib", rootPath + "/policy"}}
	if got := rootImportViolations(root); !slices.Equal(got, []string{
		rootPath + " imports example.com/lib: the root package may import the standard library only",
		rootPath + " imports " + rootPath + "/policy: the root package may import the standard library only",
	}) {
		t.Errorf("the root rule reports %q, want the two non-standard imports", got)
	}

	deps := []string{"runtime", "github.com/casbin/casbin/v3", "github.com/gin-gonic/gin", "gorm.io/gorm", rootPath + "/guard", rootPath}
	wantClosure := []string{
		rootPath + "'s closure holds github.com/casbin/casbin/v3: the root package's closure is the standard library",
		rootPath + "'s closure holds github.com/gin-gonic/gin: the root package's closure is the standard library",
		rootPath + "'s closure holds gorm.io/gorm: the root package's closure is the standard library",
		rootPath + "'s closure holds " + rootPath + "/guard: the root package imports none of this directory's subpackages",
	}
	slices.Sort(wantClosure)
	if got := closureViolations(deps); !slices.Equal(got, wantClosure) {
		t.Errorf("the closure rule reports %q, want the four forbidden entries", got)
	}

	pkgs := []listed{
		{ImportPath: authztestPath, Imports: []string{rootPath}},
		{ImportPath: rootPath + "/policy", Imports: []string{authztestPath}},
		{ImportPath: moduleImportPath + "/settings", Imports: []string{"context"}},
	}
	if got := authztestViolations(pkgs); !slices.Equal(got, []string{rootPath + "/policy imports authztest outside a test file"}) {
		t.Errorf("the fixture rule reports %q, want the one non-test importer", got)
	}
}
