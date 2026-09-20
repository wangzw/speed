package config

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"
)

// TestConfigRootCarriesOnlyTheParsingTools is the executable form of the
// boundary in this module's AGENTS.md: the root package carries the parsing
// tools and nothing else third-party. Every module that declares
// configuration imports this package, so that budget is spent for all of them
// at once, and spending it again needs the same kind of decision that spent it
// the first time.
//
// The criterion is the direct requirements rather than the whole dependency
// tree. What this module brought in is what it asked for; the transitive set
// belongs to the tools and drifts with them, and a list of it here would be a
// snapshot masquerading as a rule.
//
// They are read out of go.mod itself rather than asked of the build list,
// which in a workspace is the workspace's: it holds every module the other
// modules of this repository require, and judging those against the list
// below would fail on dependencies this module never asked for.
func TestConfigRootCarriesOnlyTheParsingTools(t *testing.T) {
	allowed := []string{
		"github.com/go-viper/mapstructure/v2",
		"github.com/spf13/cobra",
		"github.com/spf13/pflag",
		"github.com/spf13/viper",
		"github.com/vislake/speed/pkg/core",
		// The YAML parser belongs to the format/yaml subpackage, which shares
		// this go.mod with the root package. A host that does not import that
		// subpackage does not carry it.
		"go.yaml.in/yaml/v3",
	}

	cmd := exec.Command("go", "mod", "edit", "-json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("reading this module's go.mod failed: %v\n%s", err, stderr.String())
	}
	var mod struct {
		Require []struct {
			Path     string
			Indirect bool
		}
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		t.Fatalf("reading this module's go.mod failed: %v\n%s", err, out)
	}

	direct := 0
	for _, req := range mod.Require {
		if req.Indirect {
			continue
		}
		direct++
		if !slices.Contains(allowed, req.Path) {
			t.Errorf("this module requires %s, which is not one of the parsing tools the "+
				"budget was spent on: %v", req.Path, allowed)
		}
	}
	// The positive control. A go.mod with no direct requirement at all would
	// pass every judgement above without one of them having been made.
	if direct == 0 {
		t.Fatal("go.mod names no direct requirement, so the scan above proved nothing")
	}
}
