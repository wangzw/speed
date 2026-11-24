package authztest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// envPrefix is an environment-variable prefix no machine carries variables
// under, so the body is the only primary source.
const envPrefix = "SPEEDAUTHZTEST"

// ConfigReader builds the configuration reader a case hands a module under test:
// the real loader over a JSON primary source holding body, with module
// registered alongside the two modules the loader needs.
func ConfigReader(t testing.TB, module core.Module, body string) config.Reader {
	t.Helper()
	return ConfigReaderAll(t, body, module)
}

// ConfigReaderAll is ConfigReader for a case that assembles several modules.
func ConfigReaderAll(t testing.TB, body string, modules ...core.Module) config.Reader {
	t.Helper()

	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("authztest: writing the primary config source: %v", err)
	}

	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: envPrefix, DefaultLocator: "file://" + locator}},
	})
	for _, module := range modules {
		reg.Register(module)
	}

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("authztest: loading the configuration for this case: %v", err)
	}
	return reader
}
