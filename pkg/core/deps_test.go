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

const (
	moduleImportPath = "github.com/vislake/speed/pkg"
	coreRootPath     = moduleImportPath + "/core"
	coretestPath     = coreRootPath + "/coretest"
)

// TestCoreHasNoThirdPartyDependencies checks that every package core depends on is in the standard
// library or in core itself.
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

type listed struct {
	ImportPath string
	Imports    []string
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

// TestNoProductionCodeImportsCoretest is the executable form of the fixture's packaging limit:
// coretest is for tests, and the compiler cannot stop a package's non-test code from importing it.
func TestNoProductionCodeImportsCoretest(t *testing.T) {
	pkgs := goList(t)
	if !slices.ContainsFunc(pkgs, func(p listed) bool { return p.ImportPath == coretestPath }) {
		t.Fatalf("the listing does not hold %s, so the fixture rule has nothing to read", coretestPath)
	}
	for _, v := range coretestViolations(pkgs) {
		t.Error(v)
	}
}

// TestTheCoretestRuleFailsOnAForbiddenImport checks that a non-test import of coretest outside its
// own package is reported as a violation.
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
