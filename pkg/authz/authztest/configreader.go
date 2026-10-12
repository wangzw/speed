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

// envPrefix is the environment-variable prefix the host identity of the reader
// declares. It is one no machine carries variables under, so nothing outside the
// case reaches the values: the locator is the default and the body is the only
// primary source.
const envPrefix = "SPEEDAUTHZTEST"

// ConfigReader builds the configuration reader a case hands a module under test:
// the real loader over a JSON primary source holding body, with module
// registered alongside the two modules the loader needs.
//
// It is the loader itself and not a stand-in, for the reason the authentication
// fixture gives: half of what a module's configuration cases hold belongs to the
// declaration — that an item the module declared required is refused at all
// comes from the item being declared required, and a stand-in reader agreeing
// with any declaration would hold nothing. It is the loading step on its own,
// with no lifecycle driven to get at it.
func ConfigReader(t testing.TB, module core.Module, body string) config.Reader {
	t.Helper()
	return ConfigReaderAll(t, body, module)
}

// ConfigReaderAll is ConfigReader for a case that assembles several modules,
// each of which reads its own section of the one body.
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
