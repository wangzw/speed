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

// corsPackage is the subpackage of this module that bridges gin-contrib/cors.
// It is listed in subpackagesInBudget and nowhere else.
const corsPackage = repoPrefix + "pkg/http/cors"

// openapiGeneratorPackage is the subpackage that makes the fragment generator
// importable to modules outside pkg. The implementation stays in
// pkg/internal/openapigen; a host under starters/ is outside the tree the
// internal rule admits, and its own fragment is still a build product of the
// same declarations.
const openapiGeneratorPackage = repoPrefix + "pkg/http/openapigen"

// subpackagesInBudget are the packages under this directory that carry a
// dependency face of their own: a subpackage that bridges a library the root
// package does not reach. Each is named here so that its own added closure can
// be put into the budget, and the naming is the deliberate edit the ./... scan
// forces.
//
// The indirection is the price of the guard holding without the root package's
// own face having to grow to cover a subpackage's: the root package stays a
// wrapper of gin and pkg/config, and a subpackage that bridges more says so
// here, by name. A new subpackage that brings a library is reached by the ./...
// scan, is named nowhere here, and so its library falls outside every clause of
// the check and the case fails until it is listed. That is the same edit the
// guard asks for when the root package reaches a new library, and the point of
// keeping the scan over ./... rather than over the root package alone.
var subpackagesInBudget = []string{
	corsPackage,
	openapiGeneratorPackage,
}

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
// ADR-config-parsing; it builds on gin, which brings gin's
// dependencies, and the cost is recorded in ADR-http-surface. What
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
// either. A subpackage that brings a library of its own is named in
// subpackagesInBudget, so the closure it adds is weighed here too; leaving it
// unnamed would put the library it bridges outside every clause below.
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
	// A subpackage that bridges a library of its own contributes the packages it
	// reaches beyond what the root package does, and only those: the root
	// package's face is the budget already built, and every subpackage shares all
	// of it. The name is checked against the scan, so a stale one — a subpackage
	// renamed or removed — cannot quietly contribute nothing while the budget it
	// was there to widen is read as complete.
	rootClosure := dependenciesOf(t, repoPrefix+"pkg/http")
	for _, sub := range subpackagesInBudget {
		if !scanned[sub] {
			t.Fatalf("the ./... scan does not hold %s, named as a subpackage of this directory: "+
				"either it was renamed or removed and this name is stale, or the scan no longer "+
				"reaches it — and either way the closure it was named for is being let through "+
				"under a claim that no longer holds", sub)
		}
		for pkg := range dependenciesOf(t, sub) {
			if !rootClosure[pkg] {
				budget[pkg] = true
			}
		}
	}

	// The budget is built out of closures, and a closure could one day
	// contain a parser: it would then be inside the budget and no dependency
	// check could ever object to it.
	for pkg := range budget {
		for _, library := range openAPILibraries {
			if strings.HasPrefix(pkg, library) {
				t.Errorf("%s is inside the budget, through the closure of %s, of %s, or of a "+
					"subpackage named in subpackagesInBudget. An OpenAPI parsing library there "+
					"makes the shape check of a Spec the thing it is not allowed to be",
					pkg, configPackage, ginPackage)
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
			"pkg/config costs, nor part of gin's closure, nor part of what a subpackage named "+
			"in subpackagesInBudget brings. This module is a wrapper of gin, reads its "+
			"configuration through pkg/config, and adds no dependency of its own beyond what a "+
			"named subpackage bridges", pkg)
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
