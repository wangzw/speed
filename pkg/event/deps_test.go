package event_test

import (
	"os/exec"
	"strings"
	"testing"
)

const repoPrefix = "github.com/vislake/speed/"

// TestRootPackageImportsNoBackendClient is the executable form of the
// boundary the module's own documentation draws.
func TestRootPackageImportsNoBackendClient(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control. This package reaches pkg/core and pkg/config, so a
	// listing that lost them came from a load that did not happen, and every
	// negative assertion below would pass on an empty list.
	if !root[repoPrefix+"pkg/core"] {
		t.Fatalf("the root package's dependency list does not hold pkg/core: what is checked below is " +
			"not this package's dependency face")
	}

	withinBudget := map[string]bool{}
	for pkg := range dependenciesOf(t, repoPrefix+"pkg/config") {
		withinBudget[pkg] = true
	}
	for pkg := range root {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), withinBudget[pkg]:
			continue
		}
		t.Errorf("the root package depends on %s. Reading configuration costs pkg/config and its own "+
			"closure, and nothing besides: a backend client belongs to the implementation subpackage "+
			"that binds it, so that a host compiling this package carries no backend but the one it "+
			"deploys on", pkg)
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
