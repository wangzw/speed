package objectstore_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// moduleImportPath is this repository's pkg module path. Every package of it is
// inside the budget below: a package here is imported as a module of this
// repository.
const moduleImportPath = "github.com/vislake/speed/pkg"

// objectstoreRootPath is the capability's own root package, whose non-test
// dependency face a host compiles by importing it.
const objectstoreRootPath = moduleImportPath + "/objectstore"

// upperLayerPrefixes are the feature directories the root package must not
// reach: it carries the byte seam, the metadata seam, the common types and the
// backend factory, and none of the storage, authorization, tenant or HTTP
// surfaces belongs to it. The metadata module takes those up, not the interface
// package a caller and a backend both import.
var upperLayerPrefixes = []string{
	moduleImportPath + "/authz",
	moduleImportPath + "/db",
	moduleImportPath + "/http",
	moduleImportPath + "/tenant",
}

// TestRootClosureHoldsNoObjectStoreSDK is the executable form of the
// capability's dependency-direction boundary: the root package carries the
// interfaces and reaches a backend only through the module factory, so no object
// storage SDK is in what a host compiles by importing this package — it is bound
// by the backend subpackage that deploys on it — and no upper-layer feature
// directory is reached either.
//
// Nothing holds that split at compile time: a backend subpackage and the root
// package are one module. This case is the gate that keeps it. The observation
// is the root package's own non-test dependency face; the closure of a package a
// case imports is not what a host pays for.
func TestRootClosureHoldsNoObjectStoreSDK(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control: this package reaches pkg/core, so a listing that
	// lost it came from a load that did not happen and every negative assertion
	// below would pass on an empty list.
	if !root[moduleImportPath+"/core"] {
		t.Fatalf("the root package's dependency list does not hold pkg/core: what is checked below is " +
			"not this package's dependency face")
	}

	// The budget is the standard library, this repository's modules, and the
	// closures of the packages the root package legitimately takes up — config
	// for reading a backend's values, core for the registry, log for the
	// optional logging capability. A backend SDK is anything outside that.
	budget := map[string]bool{}
	for _, pkg := range []string{moduleImportPath + "/config", moduleImportPath + "/core", moduleImportPath + "/log"} {
		for reached := range dependenciesOf(t, pkg) {
			budget[reached] = true
		}
	}

	for _, pkg := range offenders(root, budget) {
		t.Errorf("the root package depends on %s: %s", pkg, reason(pkg))
	}
}

// TestTheGuardReportsASDKAndAnUpperLayer holds the case above to be a guard: a
// listing carrying an object-storage SDK or an upper-layer feature directory is
// reported, while one carrying only the standard library, this repository's
// packages and the budget closures is not. Without it the assertion above could
// pass on a listing it never actually read.
func TestTheGuardReportsASDKAndAnUpperLayer(t *testing.T) {
	// A budget entry standing for a third-party package the root legitimately
	// takes up through one of the packages named above.
	const budgeted = "github.com/spf13/viper"
	budget := map[string]bool{budgeted: true}

	clean := map[string]bool{
		objectstoreRootPath:                    true,
		"context":                              true,
		"time":                                 true,
		"internal/abi":                         true,
		moduleImportPath + "/core":             true,
		moduleImportPath + "/internal/literal": true,
		budgeted:                               true,
	}
	if got := offenders(clean, budget); len(got) != 0 {
		t.Errorf("the standard library, this repository and the budget closures are reported as "+
			"offending: %v", got)
	}

	sdk := "github.com/minio/minio-go/v7"
	got := offenders(map[string]bool{objectstoreRootPath: true, "context": true, sdk: true}, budget)
	if !slices.Equal(got, []string{sdk}) {
		t.Errorf("an object-storage SDK is not reported exactly once: %v", got)
	}

	upper := moduleImportPath + "/db"
	got = offenders(map[string]bool{objectstoreRootPath: true, "time": true, upper: true}, budget)
	if !slices.Equal(got, []string{upper}) {
		t.Errorf("an upper-layer feature directory is not reported exactly once: %v", got)
	}
}

// offenders lists, without repetition and in order, the packages in a dependency
// listing the root package must not hold: an upper-layer feature directory, or a
// package that is neither the standard library, this repository, nor a package
// of a budget closure.
func offenders(reached, budget map[string]bool) []string {
	var out []string
	for pkg := range reached {
		if underAny(pkg, upperLayerPrefixes) {
			out = append(out, pkg)
			continue
		}
		if stdlibPackage(pkg) || strings.HasPrefix(pkg, moduleImportPath+"/") || budget[pkg] {
			continue
		}
		out = append(out, pkg)
	}
	slices.Sort(out)
	return out
}

// reason says why the root package must not hold a package.
func reason(pkg string) string {
	if underAny(pkg, upperLayerPrefixes) {
		return "an upper-layer feature directory belongs to the module that takes it up, not to the " +
			"interface package a caller and a backend both import"
	}
	return "an object-storage SDK belongs to the backend subpackage that binds it, so that a host " +
		"compiling this package carries no backend but the one it deploys on"
}

// underAny reports whether path is one of the roots or sits under one.
func underAny(path string, roots []string) bool {
	return slices.ContainsFunc(roots, func(root string) bool {
		return path == root || strings.HasPrefix(path, root+"/")
	})
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
