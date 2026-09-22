package config

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestConfigRootCarriesOnlyTheParsingTools is the executable form of the
// boundary in this module's AGENTS.md: the root package carries the parsing
// tools and nothing else third-party. Every module that declares
// configuration imports this package, so that budget is spent for all of them
// at once, and spending it again needs the same kind of decision that spent it
// the first time.
//
// The observation is the root package's own non-test import face: what a host
// compiles by importing this package, and no more. It is read out of the
// package rather than out of go.mod because one go.mod now covers the whole of
// pkg/: its requirements are the union of every capability's, and judging that
// list would fail on dependencies this package never asked for. The cost of
// having no compiler support here is recorded in ADR-module-packaging-2026-09-22.
func TestConfigRootCarriesOnlyTheParsingTools(t *testing.T) {
	allowed := []string{
		"github.com/go-viper/mapstructure/v2",
		"github.com/spf13/cobra",
		"github.com/spf13/pflag",
		"github.com/spf13/viper",
		"github.com/vislake/speed/pkg/core",
	}

	imports := packageImports(t, ".")
	// The positive control. A listing produced by a load that did not happen
	// would pass every judgement below without one of them having been made.
	if !slices.Contains(imports, "github.com/vislake/speed/pkg/core") {
		t.Fatalf("the root package's import list does not hold pkg/core, so what is checked below is "+
			"not this package's import face: %v", imports)
	}

	for _, pkg := range imports {
		if stdlibPackage(pkg) || slices.Contains(allowed, pkg) {
			continue
		}
		t.Errorf("the root package imports %s, which is not one of the parsing tools the budget was "+
			"spent on: %v", pkg, allowed)
	}
}

// packageImports lists the packages a non-test build of target imports. The
// imports its test files make are separate fields of the same listing, and an
// import a case makes is not what a host pays for.
func packageImports(t *testing.T, target string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing %s's imports failed: %v\n%s", target, err, stderr.String())
	}
	var listed struct {
		Imports []string
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("reading the listing of %s failed: %v\n%s", target, err, out)
	}
	if len(listed.Imports) == 0 {
		t.Fatalf("the listing of %s names no import, so there is no import face to check", target)
	}
	return listed.Imports
}

// stdlibPackage reports whether a package path names a package of the standard
// library, whose first path element never carries a dot.
func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
