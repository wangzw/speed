package db_test

import (
	"os/exec"
	"strings"
	"testing"
)

const repoPrefix = "github.com/vislake/speed/"

// TestRootPackageImportsNoDriver is the executable form of the boundary the packaging decision
// draws: the root package wraps GORM.
func TestRootPackageImportsNoDriver(t *testing.T) {
	root := dependenciesOf(t, ".")
	if !root["gorm.io/gorm"] {
		t.Fatalf("the root package's dependency list does not hold gorm.io/gorm, which is what this " +
			"package wraps: what is checked below is not this package's dependency face")
	}

	withinBudget := dependenciesOf(t, "gorm.io/gorm")
	for pkg := range dependenciesOf(t, repoPrefix+"pkg/config") {
		withinBudget[pkg] = true
	}
	for pkg := range root {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), withinBudget[pkg]:
			continue
		}
		t.Errorf("the root package depends on %s. Wrapping GORM costs gorm.io/gorm and its own "+
			"closure, reading configuration costs pkg/config and its own, and nothing besides: a "+
			"dialect driver belongs to the implementation subpackage that binds it, so that a host "+
			"compiling this package carries no engine but the one it deploys on", pkg)
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
