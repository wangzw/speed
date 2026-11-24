package notification_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const rootPath = "github.com/vislake/speed/pkg/notification"

// jobsPath is the one package of this repository the root package reaches
// besides the standard library.
const jobsPath = "github.com/vislake/speed/pkg/jobs"

// TestRootPackageDependsOnTheStandardLibraryAndTheJobsInterface is the
// executable form of the boundary the capability draws.
func TestRootPackageDependsOnTheStandardLibraryAndTheJobsInterface(t *testing.T) {
	root := dependenciesOf(t, ".")
	// The positive control: the package reaches the jobs interface package and the
	// standard library, so a listing that lost them came from a load that did not
	// happen.
	if !root["context"] || !root["errors"] || !root["time"] || !root[jobsPath] {
		t.Fatalf("the root package's dependency list does not hold the standard library it imports and the " +
			"jobs interface package, so what is checked below is not this package's dependency face")
	}

	for _, pkg := range foreignDependencies(root) {
		t.Errorf("the root package depends on %s. It carries the interfaces, the common types and the "+
			"sentinels and reaches the standard library and the jobs interface package alone: a registry, "+
			"configuration, storage, the tenant roster or another capability belongs to the subpackage that "+
			"binds it, so that a business module that only builds a notification type carries no storage and "+
			"no tenant", pkg)
	}
}

// TestTheGuardReportsARepositoryPackage holds the case above to be a
// guard.
func TestTheGuardReportsARepositoryPackage(t *testing.T) {
	clean := map[string]bool{rootPath: true, jobsPath: true, "context": true, "errors": true, "time": true, "internal/abi": true}
	if got := foreignDependencies(clean); len(got) != 0 {
		t.Errorf("the standard library, the jobs interface package and the root package itself are reported "+
			"as foreign: %v", got)
	}

	foreign := "github.com/vislake/speed/pkg/db"
	got := foreignDependencies(map[string]bool{rootPath: true, jobsPath: true, "time": true, foreign: true})
	if !slices.Equal(got, []string{foreign}) {
		t.Errorf("a repository package in the listing is not reported exactly once: %v", got)
	}
}

// foreignDependencies lists, without repetition and in order, the packages in a
// dependency listing that are neither the standard library, the jobs interface
// package, nor the root package itself.
func foreignDependencies(reached map[string]bool) []string {
	var out []string
	for pkg := range reached {
		if pkg == rootPath || pkg == jobsPath || stdlibPackage(pkg) {
			continue
		}
		out = append(out, pkg)
	}
	slices.Sort(out)
	return out
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
