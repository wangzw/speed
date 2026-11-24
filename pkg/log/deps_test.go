package log

import (
	"os/exec"
	"strings"
	"testing"
)

const repoPrefix = "github.com/vislake/speed/"

// What the module reaches is wider than what it adds.
func TestLogAddsNoThirdPartyDependency(t *testing.T) {
	reached := dependenciesOf(t, "./...")
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
