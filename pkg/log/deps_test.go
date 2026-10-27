package log

import (
	"os/exec"
	"strings"
	"testing"
)

// repoPrefix is this repository's own module path. Every module of it is
// inside the budget below: one of them is imported as a module of this
// repository, and none of them is a third-party dependency this module hands
// on to its consumers.
const repoPrefix = "github.com/vislake/speed/"

// TestLogAddsNoThirdPartyDependency is the executable form of the design's
// dependency statement: this module's own work — the handler chain, redaction,
// context storage, configuration decoding, the two formats, file rotation and
// pruning — is done within log/slog, context, io and os, so nothing this
// module writes brings a library with it.
//
// What the module reaches is wider than what it adds. It reads its input items
// through pkg/config, and that module parses with cobra, viper, pflag and
// mapstructure; those travel into every host that logs, which is the cost
// ADR-config-parsing records for delegating the parsing. The
// statement this case holds is therefore about the increment: nothing outside
// the standard library, this repository and pkg/config's own closure.
//
// The budget is computed rather than listed. A library named here would have
// to be edited every time pkg/config swaps one of viper's indirect
// dependencies, and the case would then be pinning a snapshot of that closure
// instead of the property.
//
// The scan is over ./... rather than the root package alone, so a test host
// under internal/ cannot pull one in through the back door either.
func TestLogAddsNoThirdPartyDependency(t *testing.T) {
	reached := dependenciesOf(t, "./...")
	// The positive control. This module reads its configuration through
	// pkg/config, so a listing that lost it came from a load that did not
	// happen, and every assertion below would pass over nothing.
	if !reached["github.com/vislake/speed/pkg/config"] {
		t.Fatalf("the dependency listing does not hold github.com/vislake/speed/pkg/config, " +
			"which this module reads its input items through: what is checked below is not " +
			"this module's dependency face")
	}

	withinBudget := dependenciesOf(t, "github.com/vislake/speed/pkg/config")
	for pkg := range reached {
		switch {
		case stdlibPackage(pkg), strings.HasPrefix(pkg, repoPrefix), withinBudget[pkg]:
			continue
		}
		t.Errorf("log depends on %s, which is outside the standard library and outside what "+
			"pkg/config brings: the chain, redaction, context storage, the formats and the "+
			"file handling are written against the standard library alone, and this module "+
			"is imported by every module that logs, so a dependency of its own lands in "+
			"every host's dependency list", pkg)
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
