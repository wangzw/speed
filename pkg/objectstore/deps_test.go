package objectstore_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const moduleImportPath = "github.com/vislake/speed/pkg"

const objectstoreRootPath = moduleImportPath + "/objectstore"

var upperLayerPrefixes = []string{
	moduleImportPath + "/authz",
	moduleImportPath + "/db",
	moduleImportPath + "/http",
	moduleImportPath + "/tenant",
}

// TestRootClosureHoldsNoObjectStoreSDK checks that the root package's dependency closure stays
// within the standard library, this repository and the config, core and log budget.
func TestRootClosureHoldsNoObjectStoreSDK(t *testing.T) {
	root := dependenciesOf(t, ".")
	if !root[moduleImportPath+"/core"] {
		t.Fatalf("the root package's dependency list does not hold pkg/core: what is checked below is " +
			"not this package's dependency face")
	}

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

// TestTheGuardReportsASDKAndAnUpperLayer checks that offenders reports an object-storage SDK and an
// upper-layer directory once each while accepting budgeted packages.
func TestTheGuardReportsASDKAndAnUpperLayer(t *testing.T) {
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

func reason(pkg string) string {
	if underAny(pkg, upperLayerPrefixes) {
		return "an upper-layer feature directory belongs to the module that takes it up, not to the " +
			"interface package a caller and a backend both import"
	}
	return "an object-storage SDK belongs to the backend subpackage that binds it, so that a host " +
		"compiling this package carries no backend but the one it deploys on"
}

func underAny(path string, roots []string) bool {
	return slices.ContainsFunc(roots, func(root string) bool {
		return path == root || strings.HasPrefix(path, root+"/")
	})
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

func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
