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

const (
	moduleImportPath = "github.com/vislake/speed/pkg"
	rootPath         = moduleImportPath + "/authz"
	authztestPath    = rootPath + "/authztest"
)

type listed struct {
	ImportPath string
	// Imports are the imports of the package's non-test files that build here.
	Imports []string
}

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

func isStandard(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

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

// TestTheRootPackageImportsOnlyTheStandardLibrary checks that the root package imports only the standard library.
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

// TestNoProductionCodeImportsAuthztest checks that no production code imports authztest.
func TestNoProductionCodeImportsAuthztest(t *testing.T) {
	pkgs := goList(t)
	if _, ok := find(pkgs, authztestPath); !ok {
		t.Fatalf("the listing does not hold %s, so the fixture rule has nothing to read", authztestPath)
	}
	for _, v := range authztestViolations(pkgs) {
		t.Error(v)
	}
}

// TestTheRulesFailOnAViolation checks that the rules fail on a violation.
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
