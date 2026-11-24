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

const configPackage = repoPrefix + "pkg/config"

const ginPackage = "github.com/gin-gonic/gin"

const corsPackage = repoPrefix + "pkg/http/cors"

const openapiGeneratorPackage = repoPrefix + "pkg/http/openapigen"

var subpackagesInBudget = []string{
	corsPackage,
	openapiGeneratorPackage,
}

var openAPILibraries = []string{
	"github.com/getkin/kin-openapi",
	"github.com/go-openapi/",
	"github.com/pb33f/libopenapi",
	"github.com/oapi-codegen/",
	"github.com/swaggo/",
	"github.com/google/gnostic",
}

// TestHTTPAddsNoDependencyBeyondItsAnchors checks the dependency budget: this module
// is a wrapper of gin and reads its configuration through pkg/config.
func TestHTTPAddsNoDependencyBeyondItsAnchors(t *testing.T) {
	scanned := dependenciesOf(t, "./...")
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
