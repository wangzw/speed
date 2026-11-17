package core

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
	coreRootPath     = moduleImportPath + "/core"
	coretestPath     = coreRootPath + "/coretest"
)

// TestCoreHasNoThirdPartyDependencies is the executable form of the first
// architecture invariant: core is imported by every module, so any dependency
// of its own would land in every host's dependency list. A standard library
// path has no domain in its first segment; anything else does.
func TestCoreHasNoThirdPartyDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" || strings.HasPrefix(pkg, coreRootPath) {
			continue
		}
		first, _, _ := strings.Cut(pkg, "/")
		if strings.Contains(first, ".") {
			t.Errorf("core depends on %s, which is outside the standard library", pkg)
		}
	}
}

// listed is what these checks read about one package: its import path and the
// imports of its non-test files. go list reports the test files' imports
// separately, and the rule is about production code, so those are not asked
// for.
type listed struct {
	ImportPath string
	Imports    []string
}

// goList lists every package of the module with go list and returns them.
// The pattern names the module, so the listing does not depend on the
// directory the tests run from. -e keeps a package that does not compile in
// the listing, with its imports as the parser read them.
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

// TestNoProductionCodeImportsCoretest is the executable form of the fixture's
// packaging limit: coretest is for tests, and the compiler cannot stop a
// package's non-test code from importing it. A package that did would carry
// the fixture into a binary a host ships.
//
// The listing is go list's, and a positive control holds that it is reading
// this tree: a listing without coretest in it would let the rule pass while
// looking at nothing.
func TestNoProductionCodeImportsCoretest(t *testing.T) {
	pkgs := goList(t)
	if !slices.ContainsFunc(pkgs, func(p listed) bool { return p.ImportPath == coretestPath }) {
		t.Fatalf("the listing does not hold %s, so the fixture rule has nothing to read", coretestPath)
	}
	for _, v := range coretestViolations(pkgs) {
		t.Error(v)
	}
}

// TestTheCoretestRuleFailsOnAForbiddenImport holds the rule to be a rule and
// not a check that accepts anything: an import of coretest from another
// package's non-test code is reported, while coretest's own files and a
// standard-library import of an ordinary package are not.
func TestTheCoretestRuleFailsOnAForbiddenImport(t *testing.T) {
	pkgs := []listed{
		{ImportPath: coretestPath, Imports: []string{"strings"}},
		{ImportPath: coreRootPath, Imports: []string{"strings"}},
		{ImportPath: moduleImportPath + "/settings", Imports: []string{coretestPath}},
	}
	got := coretestViolations(pkgs)
	want := []string{moduleImportPath + "/settings imports coretest outside a test file"}
	if !slices.Equal(got, want) {
		t.Fatalf("violations = %v, want %v", got, want)
	}
}

// coretestViolations reports every package whose non-test files import
// coretest, coretest itself excepted.
func coretestViolations(pkgs []listed) []string {
	var out []string
	for _, p := range pkgs {
		if p.ImportPath == coretestPath {
			continue
		}
		if slices.Contains(p.Imports, coretestPath) {
			out = append(out, p.ImportPath+" imports coretest outside a test file")
		}
	}
	slices.Sort(out)
	return out
}
