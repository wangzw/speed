// Package configreader builds the configuration reader a case hands a module
// under test.
//
// It lives in the capability directory's internal subtree because the gateway
// and the static backend both need it, and a helper written in one package's
// tests is out of reach of the other's.
package configreader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
)

// Over builds the reader a real assembly hands module: the actual configuration
// loader over a JSON primary source holding body, with module registered
// alongside the two modules that make the loader work.
//
// It is the loader itself rather than a stand-in because half of what these
// cases hold belongs to the declaration rather than to the code — that an absent
// item the module declared required is refused at all comes from the item being
// declared required, and a stand-in reader agreeing with any declaration
// whatsoever would hold nothing. It is the loading step on its own, which is
// what these cases want: the reader, with no lifecycle driven to get at it. The
// command line is given as empty and the prefix is one no machine carries
// variables under, so the locator is the host identity's default and nothing
// outside the case reaches the values.
func Over(t *testing.T, module core.Module, body string) config.Reader {
	t.Helper()
	return OverAll(t, body, module)
}

// OverAll is Over for a case that assembles several modules, each of which
// reads its own section of the one body.
func OverAll(t *testing.T, body string, modules ...core.Module) config.Reader {
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
		Resources: []any{config.HostIdentity{Prefix: "SPEEDAUTHNTEST", DefaultLocator: "file://" + locator}},
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
