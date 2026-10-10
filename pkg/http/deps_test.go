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

// ginPackage is the package this module is a wrapper of, and whose closure a
// host that takes this module up therefore takes up too.
const ginPackage = "github.com/gin-gonic/gin"

// openAPILibraries are the module paths of libraries that parse OpenAPI
// documents. The module checks the shape of a fragment and nothing more, and
// parsing one would need a library from this list or one like it.
var openAPILibraries = []string{
	"github.com/getkin/kin-openapi",
	"github.com/go-openapi/",
	"github.com/pb33f/libopenapi",
	"github.com/oapi-codegen/",
	"github.com/swaggo/",
	"github.com/google/gnostic",
}

// TestHTTPAddsNoDependencyBeyondItsAnchors is the executable form of the
// design's dependency statement: this module is a wrapper of gin and reads its
// configuration through pkg/config, so a host that takes it up takes on the
// closure of both, and that is all it takes on. It is also what keeps the Spec
// check to a shape check — an OpenAPI parsing library would land here the
// moment one is imported.
//
// The budget is the standard library, this repository's modules, the closure of
// pkg/config and the closure of gin. This package declares its input items to
// config and reads its endpoints through config's reader, which brings the
// tools config parses configuration with, and the cost is recorded in
// ADR-config-parsing-2026-09-20; it builds on gin, which brings gin's
// dependencies, and the cost is recorded in ADR-http-surface-2026-09-14. What
// still holds, and what is checked here, is that this package adds nothing of
// its own to either.
//
// Both closures are read as they stand rather than written out as lists of
// library names. gin or config exchanging one indirect dependency for another
// changes nothing about this package's face, and a written-out list would turn
// every such exchange into an edit here.
//
// The scan is over ./... rather than the root package alone, so a package added
// under this directory cannot bring a library in without this case seeing it
// either.
func TestHTTPAddsNoDependencyBeyondItsAnchors(t *testing.T) {
	scanned := dependenciesOf(t, "./...")
	// The positive controls. This package is in its own dependency listing,
	// and a listing that lost it came from a load that did not happen —
	// every judgement below would then pass on an empty set. It reaches gin
	// as well: a package that stopped importing gin would leave the gin half
	// of the budget standing for nothing.
	if !scanned[repoPrefix+"pkg/http"] {
		t.Fatalf("the scan does not hold %spkg/http, so what it read is not this module's "+
			"dependency face", repoPrefix)
	}
	if !scanned[ginPackage] {
		t.Fatalf("the scan does not hold %s, so this module is not what the budget below says it is: "+
			"a wrapper of gin", ginPackage)
	}

	budget := dependenciesOf(t, configPackage)
	for pkg := range dependenciesOf(t, ginPackage) {
		budget[pkg] = true
	}

	// The budget is built out of two closures, and a closure could one day
	// contain a parser: it would then be inside the budget and no dependency
	// check could ever object to it.
	for pkg := range budget {
		for _, library := range openAPILibraries {
			if strings.HasPrefix(pkg, library) {
				t.Errorf("%s is inside the budget, through the closure of %s or of %s. An OpenAPI "+
					"parsing library there makes the shape check of a Spec the thing it is not "+
					"allowed to be", pkg, configPackage, ginPackage)
			}
		}
	}

	for pkg := range scanned {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), budget[pkg]:
			continue
		}
		t.Errorf("this module depends on %s, which is neither the standard library, nor a "+
			"module of this repository, nor part of what reading configuration through "+
			"pkg/config costs, nor part of gin's closure. This module is a wrapper of gin and "+
			"adds no dependency of its own to what gin and pkg/config bring", pkg)
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
