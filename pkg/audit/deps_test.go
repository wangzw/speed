package audit_test

import (
	"os/exec"
	"strings"
	"testing"
)

const rootPackage = "github.com/vislake/speed/pkg/audit"

// TestRootPackageImportsTheStandardLibraryAlone is the executable form of
// the boundary the module's own documentation draws.
func TestRootPackageImportsTheStandardLibraryAlone(t *testing.T) {
	root := dependenciesOf(t, ".")

	// The positive control. This package uses context and errors, so a listing
	// that lost them came from a load that did not happen, and the assertion
	// below would pass on an empty list.
	if !root["context"] {
		t.Fatalf("the root package's dependency list does not hold the standard library's context: " +
			"what is checked below is not this package's dependency face")
	}

	for pkg := range root {
		if pkg == rootPackage {
			continue
		}
		if !stdlibPackage(pkg) {
			t.Errorf("the root package depends on %s. It carries the interface, the entry type and the "+
				"capture-path names and nothing else, so that a module naming the anchor pays for a "+
				"standard-library import; a type or a helper that needs more belongs in the journal "+
				"subpackage", pkg)
		}
	}
}

func dependenciesOf(t *testing.T, target string) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", target).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s failed: %v\n%s", target, err, out)
	}
	reached := make(map[string]bool)
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if pkg := strings.TrimSpace(line); pkg != "" {
			reached[pkg] = true
		}
	}
	if len(reached) == 0 {
		t.Fatalf("go list -deps %s listed nothing, so there is no dependency face to check", target)
	}
	return reached
}

// stdlibPackage reports whether a package path names a package of the standard
// library, whose first path element never carries a dot.
func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
