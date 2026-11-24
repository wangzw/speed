package outbox_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTheInterfacePackageCostsTheEventPackageAndGORMAlone is the
// executable form of the boundary this package's documentation draws.
func TestTheInterfacePackageCostsTheEventPackageAndGORMAlone(t *testing.T) {
	face := dependenciesOf(t, ".")
	// The positive control. This package names *gorm.DB, so a listing without
	// GORM came from a load that did not happen, and the assertion below would
	// pass on an empty list.
	if !face["gorm.io/gorm"] {
		t.Fatalf("the interface package's dependency list does not hold GORM: what is checked below is " +
			"not this package's dependency face")
	}
	budget := map[string]bool{}
	for _, target := range []string{"github.com/vislake/speed/pkg/event", "gorm.io/gorm"} {
		for pkg := range dependenciesOf(t, target) {
			budget[pkg] = true
		}
	}
	for pkg := range face {
		if pkg == "github.com/vislake/speed/pkg/event/outbox" || budget[pkg] || stdlibPackage(pkg) {
			continue
		}
		t.Errorf("the interface package depends on %s, beyond the event package and GORM. A driver or "+
			"a bus's client belongs to the relay subpackage, which a host registers; every module that "+
			"stages an event imports this one", pkg)
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
