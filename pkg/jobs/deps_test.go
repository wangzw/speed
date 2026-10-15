package jobs_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// rootPath is this package's own import path. `go list -deps` lists the target
// package together with what it reaches, so the listing holds this path itself;
// that is the package under check, not a dependency of it.
const rootPath = "github.com/vislake/speed/pkg/jobs"

// TestRootPackageDependsOnTheStandardLibraryAlone is the executable form of the
// boundary the capability draws: the root package carries the interface, the
// common types and the sentinels, and imports nothing of this repository — not
// the registry, not configuration, not the tenant roster. A module that only
// submits a job therefore depends on this package alone, without carrying
// storage or the tenant.
//
// Nothing holds that split at compile time: an implementation subpackage and the
// root package are one module. This case is the gate that keeps it. The
// observation is the root package's own non-test dependency face — what a host
// compiles by importing this package, and no more; an import a case in this
// package makes is not what a host pays for, and moving one into a non-test file
// is the mistake this catches.
func TestRootPackageDependsOnTheStandardLibraryAlone(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control: this package imports context and time from the
	// standard library, so a listing that lost them came from a load that did
	// not happen, and the negative assertions below would pass on an empty list.
	if !root["context"] || !root["time"] {
		t.Fatalf("the root package's dependency list does not hold the standard library it imports, so " +
			"what is checked below is not this package's dependency face")
	}

	for _, pkg := range foreignDependencies(root) {
		t.Errorf("the root package depends on %s. It carries the interface, the common types and the "+
			"sentinels and reaches the standard library alone: a backend, the tenant roster, or any other "+
			"package of this repository belongs to the subpackage that binds it, so that a module that only "+
			"submits a job carries no storage and no tenant", pkg)
	}
}

// TestTheGuardReportsARepositoryPackage holds the case above to be a guard: a
// listing carrying a package of this repository other than the root package is
// reported, while one carrying only the standard library and the root package
// itself is not. Without it the assertion above could pass on a listing it never
// actually read.
func TestTheGuardReportsARepositoryPackage(t *testing.T) {
	clean := map[string]bool{rootPath: true, "context": true, "time": true, "internal/abi": true}
	if got := foreignDependencies(clean); len(got) != 0 {
		t.Errorf("the standard library and the root package itself are reported as foreign: %v", got)
	}

	foreign := "github.com/vislake/speed/pkg/config"
	got := foreignDependencies(map[string]bool{rootPath: true, "time": true, foreign: true})
	if !slices.Equal(got, []string{foreign}) {
		t.Errorf("a repository package in the listing is not reported exactly once: %v", got)
	}
}

// foreignDependencies lists, without repetition and in order, the packages in a
// dependency listing that are neither the standard library nor the root package
// itself.
func foreignDependencies(reached map[string]bool) []string {
	var out []string
	for pkg := range reached {
		if pkg == rootPath || stdlibPackage(pkg) {
			continue
		}
		out = append(out, pkg)
	}
	slices.Sort(out)
	return out
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
