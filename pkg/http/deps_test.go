package http

import (
	"os/exec"
	"strings"
	"testing"
)

// repoPrefix is this repository's own module path. Every module of it is
// inside the budget below: what this case weighs is what a host takes on
// besides this repository's own code.
const repoPrefix = "github.com/vislake/speed/"

// configPackage is the package whose closure this one inherits by reading its
// configuration through it.
const configPackage = repoPrefix + "pkg/config"

// TestHTTPAddsNoThirdPartyDependency is the executable form of the design's
// surface statement: the registration surface carries net/http's types and
// this package's types and nothing else, so a host that registers a route
// takes on no routing library. It is also what keeps the Spec check to a shape
// check — an OpenAPI parsing library would land here the moment one is
// imported, and so would a routing library behind the seam.
//
// The budget is the standard library, this repository's modules, and the
// closure of pkg/config. This package declares its input items to config and
// reads its endpoints through config's reader, and taking configuration up is
// taking up the tools config parses it with: they reach a host from there,
// through this package, and the cost is recorded in
// ADR-config-parsing-2026-09-20. What still holds, and what is checked here,
// is that this package adds nothing of its own to that.
//
// The budget is config's closure as it stands rather than a written-out list
// of library names. config exchanging one indirect dependency for another
// changes nothing about this package's face, and a written-out list would turn
// every such exchange into an edit here.
//
// The scan is over ./... rather than the root package alone, so an
// implementation subpackage cannot bind an engine from a routing library
// without this case seeing it either.
func TestHTTPAddsNoThirdPartyDependency(t *testing.T) {
	scanned := dependenciesOf(t, "./...")
	// The positive control. This package is in its own dependency listing,
	// and a listing that lost it came from a load that did not happen —
	// every judgement below would then pass on an empty set.
	if !scanned[repoPrefix+"pkg/http"] {
		t.Fatalf("the scan does not hold %spkg/http, so what it read is not this module's "+
			"dependency face", repoPrefix)
	}

	withinBudget := dependenciesOf(t, configPackage)
	for pkg := range scanned {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), withinBudget[pkg]:
			continue
		}
		t.Errorf("this module depends on %s, which is neither the standard library, nor a "+
			"module of this repository, nor part of what reading configuration through "+
			"pkg/config costs. The surface here is net/http's types and this package's "+
			"own, and the engine behind it is bound by an implementation subpackage that "+
			"brings its own library with it", pkg)
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
