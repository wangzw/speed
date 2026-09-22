package audit_test

import (
	"os/exec"
	"strings"
	"testing"
)

// rootPackage is this module's interface package, the target of the listing
// below and the one entry in it that is not a dependency.
const rootPackage = "github.com/vislake/speed/pkg/audit"

// TestRootPackageImportsTheStandardLibraryAlone is the executable form of the
// boundary the module's own documentation draws.
//
// The root package is what a module imports to name the audit anchor on the
// entry point's middleware chain. Dependencies resolve at package granularity,
// so what this package reaches is what every module that places a layer against
// audit takes on — the cost the anchor record states and accepts. It reaches the
// standard library alone, so nothing third-party enters that module's go.mod;
// the go.sum and the `go mod tidy` fetch follow the test closures of the
// imported packages instead, and whole-graph reads see every library an
// implementation binds. The one thing that must not happen is that cost growing
// without anyone saying so: an interface package whose surface needs a database
// type has stopped being an interface package.
//
// Nothing holds this at compile time. The root package and the implementation
// subpackage are one module, so the module's dependency list carries the
// database, GORM, the event bus and the entry point whichever of them a host
// deploys on, and the split is a rule about imports. This case is the gate that
// keeps it.
//
// The observation is the root package's own non-test dependency face. The cases
// in the journal subpackage import real backends to have something to run
// against, and an import a case makes is not what a host pays for.
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
			// `go list -deps` lists the target among its own dependencies.
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

// dependenciesOf lists the packages a `go list -deps` target reaches.
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
