package board

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	moduleImportPath = "github.com/vislake/speed/pkg"
	boardPath        = moduleImportPath + "/feature/board"
)

// allowedImports are the root packages this module may reach. The authentication
// and authorization capabilities are reached through their root packages alone:
// their subpackages bind an implementation, and this module is above both.
var allowedImports = map[string]bool{
	"github.com/gin-gonic/gin":    true,
	moduleImportPath + "/authn":   true,
	moduleImportPath + "/authz":   true,
	moduleImportPath + "/config":  true,
	moduleImportPath + "/core":    true,
	moduleImportPath + "/feature": true,
	moduleImportPath + "/http":    true,
	moduleImportPath + "/log":     true,
}

// listed is what these checks read about one package: its import path and the
// imports of its non-test files. go list reports test imports separately, and
// the rules are about production code, so those are not asked for.
type listed struct {
	ImportPath string
	Imports    []string
}

// goList lists every package of the module with go list. The pattern names the
// module, so the listing does not depend on the directory the tests run from.
// -e keeps a package that does not compile in the listing, with its imports as
// the parser read them.
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
			if err == io.EOF {
				return pkgs
			}
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
}

// TestTheBoardImportsTheRootPackagesAndNothingElse holds the module's dependency
// position: it sits above pkg/http and above the authentication and
// authorization root packages, so it may reach those roots and the shared
// infrastructure, and no implementation subpackage of either capability.
func TestTheBoardImportsTheRootPackagesAndNothingElse(t *testing.T) {
	var found bool
	for _, pkg := range goList(t) {
		if pkg.ImportPath != boardPath {
			continue
		}
		found = true
		if len(pkg.Imports) == 0 {
			t.Fatal("the board package reports no import, so this check would pass over nothing")
		}
		for _, imp := range pkg.Imports {
			if standardLibrary(imp) || allowedImports[imp] {
				continue
			}
			t.Errorf("the board imports %q. It may reach the standard library, gin, and the root "+
				"packages core, config, http, authn, authz, log and feature; an implementation "+
				"subpackage would put this module below what it is meant to sit above", imp)
		}
	}
	if !found {
		t.Fatalf("the listing does not contain %q, so the check would pass over nothing", boardPath)
	}
}

// TestNoOtherModuleImportsTheBoard holds the other direction: the board is taken
// into a command by the host naming it, and no module reaches it. A module that
// imported it would make the publisher part of every binary that imports that
// module.
func TestNoOtherModuleImportsTheBoard(t *testing.T) {
	others := 0
	for _, pkg := range goList(t) {
		if pkg.ImportPath == boardPath {
			continue
		}
		others++
		if slices.Contains(pkg.Imports, boardPath) {
			t.Errorf("package %q imports %q; the board is named on a command tree, never reached "+
				"by another module", pkg.ImportPath, boardPath)
		}
	}
	if others == 0 {
		t.Fatal("the listing holds no package beside the board, so the check would pass over nothing")
	}
}

// standardLibrary reports whether path names a standard-library package. Its
// first segment carries no dot, which every module path does.
func standardLibrary(path string) bool {
	first := path
	if i := strings.IndexByte(first, '/'); i >= 0 {
		first = first[:i]
	}
	return !strings.Contains(first, ".")
}
