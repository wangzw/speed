package config

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestConfigRootCarriesOnlyTheParsingTools checks that config root carries only the parsing tools.
func TestConfigRootCarriesOnlyTheParsingTools(t *testing.T) {
	allowed := []string{
		"github.com/go-viper/mapstructure/v2",
		"github.com/spf13/cobra",
		"github.com/spf13/pflag",
		"github.com/spf13/viper",
		"github.com/vislake/speed/pkg/core",
	}

	imports := packageImports(t, ".")
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

func stdlibPackage(pkg string) bool {
	first, _, _ := strings.Cut(pkg, "/")
	return !strings.Contains(first, ".")
}
