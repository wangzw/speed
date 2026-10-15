package s3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
)

// registryWithReader builds the minimal assembly this module's own config
// reading needs: a real configuration loader over a JSON primary source holding
// body, with this module registered so its declared items are known, supplied
// under the config module's name and registered so the descriptor can be driven.
func registryWithReader(t *testing.T, body string) *core.Registry {
	t.Helper()

	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}

	loader := core.New()
	loader.Register(jsonformat.Module())
	loader.Register(filesource.Module())
	loader.Register(core.Module{
		Name: "host",
		Resources: []any{config.HostIdentity{
			Prefix:         "SPEEDOBJECTSTORES3TEST",
			DefaultLocator: "file://" + locator,
		}},
	})
	loader.Register(Module())
	reader, err := config.Load(t.Context(), loader, nil)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	return reg
}

// completeBody is the configuration body a working assembly is built from.
func completeBody(endpoint string) string {
	return `{"objectstore":{"s3":{` +
		`"endpoint":` + mustJSON(endpoint) + `,` +
		`"region":"us-east-1",` +
		`"bucket":"` + testBucket + `",` +
		`"access-key":"` + testAccessKey + `",` +
		`"secret-key":"` + testSecretKey + `",` +
		`"use-path-style":true}}}`
}

// mustJSON encodes one JSON string, so an endpoint carrying a scheme still
// produces a valid body.
func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestTheDescriptorDeliversTheByteSeamExclusively holds the shape the design
// names: the objectstore.Store seam claimed for this module alone, so a command
// names exactly one backend, and the logging capability taken up optionally.
func TestTheDescriptorDeliversTheByteSeamExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	seam := core.Token((*objectstore.Store)(nil))
	if len(m.Provides) != 1 || m.Provides[0].Token != seam || !m.Provides[0].Exclusive {
		t.Errorf("Provides = %+v, want the byte seam alone, exclusively", m.Provides)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the one optional logging capability", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want an optional logging capability", r)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the one input-item schema", m.Resources)
	}
}

// TestPrepareStatesEnabledForARunnableConfiguration holds that a complete
// configuration lets the module state it runs.
func TestPrepareStatesEnabledForARunnableConfiguration(t *testing.T) {
	reg := registryWithReader(t, completeBody("https://s3.example.com"))
	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestTheModuleDeliversTheStoreThroughTheRegistry drives a real registry through
// this module's descriptor to Init, resolves the byte seam from the one product
// and names it.
func TestTheModuleDeliversTheStoreThroughTheRegistry(t *testing.T) {
	server := httptest.NewServer(recorder(&requestRecord{}, http.StatusOK, "ok"))
	defer server.Close()

	reg := registryWithReader(t, completeBody(endpointOf(server)))
	var store objectstore.Store
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			store, err = core.Resolve[objectstore.Store](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if store == nil || store.Name() != backendName {
		t.Errorf("the byte seam resolved %v, want one named %s", store, backendName)
	}
}

// TestTheCredentialsAreRegisteredAsRedactionRules holds that the module hands
// its credentials to the factory's secrets hook, which is where they are
// registered as literal redaction rules before the client is built.
func TestTheCredentialsAreRegisteredAsRedactionRules(t *testing.T) {
	got := secrets(&moduleConfig{AccessKey: "the-key", SecretKey: "the-secret"})
	want := map[string]bool{"the-key": true, "the-secret": true}
	if len(got) != len(want) {
		t.Fatalf("secrets = %v, want the access key and the secret", got)
	}
	for _, v := range got {
		if !want[v] {
			t.Errorf("secrets = %v, want the access key and the secret", got)
		}
	}
}
