// Package configtest builds the configuration reader a carrier case hands a
// module under test.
//
// It is the authentication capability's authntest reader without the authn
// dependency: the sending capabilities' carriers read their own sections and
// must not pull the authentication capability into their tests, so a local
// reader is what keeps the sending capabilities free of it.
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

// Reader builds the real configuration loader over a JSON primary source holding
// body, with the given modules registered so their declared items are known.
//
// It is the loader itself rather than a stand-in because half of what these
// cases hold belongs to the declaration rather than to the code — that an absent
// item a module declared required is refused at all comes from the item being
// declared required. The command line is given as empty and the prefix is one no
// machine carries variables under, so the locator is the host identity's default
// and nothing outside the case reaches the values.
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
