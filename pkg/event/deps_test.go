package event_test

import (
	"os/exec"
	"strings"
	"testing"
)

// repoPrefix is this repository's own module path. Every module of it is inside
// this package's budget: a module here is imported as a module of this
// repository, and none of them reaches a backend client.
const repoPrefix = "github.com/vislake/speed/"

// TestRootPackageImportsNoBackendClient is the executable form of the boundary
// the module's own documentation draws: the root package carries the interface
// and reaches a backend only through the constructor an implementation hands
// it, so a client belongs to the subpackage that binds it.
//
// Nothing holds that split at compile time. An implementation subpackage and the
// root package are one module, so the module's dependency list carries every
// client whichever backend a host deploys on, and the split is a rule about
// imports. This case is the gate that keeps it — the cost of having no compiler
// support here is recorded in ADR-module-layout.
//
// The observation is the root package's own non-test dependency face: what a
// host compiles by importing this package, and no more. The cases in this
// package import real clients to have a backend to run against, and an import a
// case makes is not what a host pays for; moving one of those imports into a
// non-test file is the mistake this catches.
//
// The budget is the standard library, this repository's modules, and the closure
// of pkg/config — which is what reading configuration costs, since this package
// takes its values up through it and that package parses the command line with
// third-party libraries. Anything else came in through this package, and a
// backend client is the shape that matters.
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
