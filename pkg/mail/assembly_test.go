package mail

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// completeBody is the configuration body a working assembly is built from. It is
// empty because the stub carrier accepts no item, so it declares no key.
func completeBody() string { return `{}` }

// configReader builds the real configuration loader over a JSON primary source
// holding body, with the stub module registered so its declared items are known.
//
// It is the loader itself rather than a stand-in because half of what these
// cases hold belongs to the declaration rather than to the code: the reader a
// carrier decodes against is the one config built from the modules' schemas.
func configReader(t *testing.T, body string) config.Reader {
	t.Helper()

	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "MAILTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(stubModule())

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	return reader
}

// registryWithReader builds a registry with a real configuration reader over
// body, supplied under the config module's name, which is what a module that
// reads configuration needs before its Prepare runs.
func registryWithReader(t *testing.T, body string) *core.Registry {
	t.Helper()
	reader := configReader(t, body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// TestTheModuleDeliversTheSenderThroughTheRegistry drives a real registry
// through the stub carrier's descriptor to Init, resolves the sending seam from
// the product and names it.
func TestTheModuleDeliversTheSenderThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, completeBody())
	reg.Register(stubModule())

	var delivered Sender
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{stubName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			delivered, err = core.Resolve[Sender](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if delivered == nil {
		t.Fatal("the registry resolved no sender")
	}
	if delivered.Name() != "stub" {
		t.Errorf("Name() = %q, want stub", delivered.Name())
	}
}
