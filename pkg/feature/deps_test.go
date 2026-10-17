package feature

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// rootPath is the package these checks are about.
const rootPath = "github.com/vislake/speed/pkg/feature"

// listed is what this check reads about one package: its import path and the
// imports of its non-test files. go list reports test imports separately and
// this rule is about production code, so those are not asked for.
type listed struct {
	ImportPath string
	Imports    []string
}

// goList returns the listing of the package at path.
func goList(t *testing.T, path string) listed {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Imports", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", path, err, stderr.String())
	}
	var l listed
	if err := json.Unmarshal(out, &l); err != nil {
		t.Fatalf("decoding the go list output for %s: %v", path, err)
	}
	return l
}

// TestTheRootPackageImportsTheStandardLibraryAlone holds the one dependency
// rule this package has. Every module that declares a feature imports it, so a
// third-party library reached from here would land in each of those modules'
// closures; the rule is enforced by deps_test.go rather than by convention
// because a later import looks harmless at the call site.
//
// The detector is the import path's first segment: a standard-library package
// has no dot in it and every module path does.
func TestTheRootPackageImportsTheStandardLibraryAlone(t *testing.T) {
	pkg := goList(t, rootPath)
	if pkg.ImportPath != rootPath {
		t.Fatalf("go list reported the package %q, want %q", pkg.ImportPath, rootPath)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the root package reports no import, so this check would pass over nothing; it " +
			"is meant to see at least errors and regexp")
	}
	for _, imp := range pkg.Imports {
		if strings.Contains(firstSegment(imp), ".") {
			t.Errorf("the root package imports %q, which is not a standard-library package: a "+
				"declaring module would carry it too", imp)
		}
	}
}

// TestTheDetectorRejectsAModuleImport makes the rule above discriminating: a
// run of it that accepted every import would pass this check and every later
// one without telling anyone anything.
func TestTheDetectorRejectsAModuleImport(t *testing.T) {
	for _, c := range []struct {
		path string
		want bool
	}{
		{"errors", false},
		{"net/http", false},
		{"github.com/vislake/speed/pkg/core", true},
		{"golang.org/x/text", true},
	} {
		if got := strings.Contains(firstSegment(c.path), "."); got != c.want {
			t.Errorf("the detector reports %v for %q, want %v", got, c.path, c.want)
		}
	}
}

// firstSegment returns what precedes the first slash of an import path.
func firstSegment(path string) string {
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}
