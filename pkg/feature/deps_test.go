package feature

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const rootPath = "github.com/vislake/speed/pkg/feature"

type listed struct {
	ImportPath string
	Imports    []string
}

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

// TestTheRootPackageImportsTheStandardLibraryAlone checks that the root package
// imports no third-party module.
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

// TestTheDetectorRejectsAModuleImport checks that the import-path detector tells
// a standard-library package from a module package.
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

func firstSegment(path string) string {
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}
