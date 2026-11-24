package jobs_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const rootPath = "github.com/vislake/speed/pkg/jobs"

// TestRootPackageDependsOnTheStandardLibraryAlone checks that the root package
// reaches the standard library alone.
func TestRootPackageDependsOnTheStandardLibraryAlone(t *testing.T) {
	root := dependenciesOf(t, ".")
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

// TestTheGuardReportsARepositoryPackage checks that the guard reports a
// repository package and accepts the standard library and the root package.
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
