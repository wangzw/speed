// Package configtest builds the real configuration reader a carrier case hands a module under test.
package configtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// Reader builds the real configuration loader over a JSON primary source holding body, with the
// given modules registered so their declared items are known.
func Reader(t testing.TB, body string, modules ...core.Module) config.Reader {
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
		Resources: []any{config.HostIdentity{Prefix: "SPEEDCONFIGTEST", DefaultLocator: "file://" + locator}},
	})
	for _, module := range modules {
		reg.Register(module)
	}

	reader, err := config.Load(t.Context(), reg, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	return reader
}
