package fs

import (
	"context"
	"errors"
	"fmt"
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

// hostPrefix is the environment prefix the case's host identity carries. It is
// one no machine exports variables under, so nothing outside the case reaches
// the values.
const hostPrefix = "SPEEDOBJECTSTOREFSTEST"

// bodyFor is the configuration body a working assembly is built from, over the
// given root.
func bodyFor(root string) string {
	return fmt.Sprintf(`{"objectstore.fs":{"root":%q}}`, root)
}

// loadFrom builds the real configuration loader over a JSON primary source
// holding body, with this module registered so its declared items are known. It
// answers the loader's error without failing the case, so a case about a missing
// item can read it.
func loadFrom(t testing.TB, body string) (config.Reader, error) {
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
		Resources: []any{config.HostIdentity{Prefix: hostPrefix, DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	return config.Load(t.Context(), reg, nil)
}

// registryWithReader builds a registry carrying the configuration reader this
// module reads its section from, which is what its Prepare and New need before
// they run.
func registryWithReader(t testing.TB, body string) *core.Registry {
	t.Helper()
	reader, err := loadFrom(t, body)
	if err != nil {
		t.Fatalf("loading the configuration for this case: %v", err)
	}
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// TestTheDescriptorDeliversTheByteSeamExclusively holds the shape the design
// names: the objectstore.Store claimed for this module alone, so a command names
// exactly one backend, and the logging capability taken up optionally.
func TestTheDescriptorDeliversTheByteSeamExclusively(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	seamToken := core.Token((*objectstore.Store)(nil))
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the one byte seam", m.Provides)
	}
	if p := m.Provides[0]; p.Token != seamToken || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want the byte seam delivered exclusively", p)
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

// TestTheRootItemTakesThePrimarySourceAlone holds the origin the design fixes
// for this backend's one input item: the directory is read from the primary
// configuration source alone, so neither an environment variable nor a
// command-line argument can point a process's bytes elsewhere. A declared item
// left at the zero origin would silently accept the environment as well, which
// is what the assertion below tells apart.
func TestTheRootItemTakesThePrimarySourceAlone(t *testing.T) {
	var schema config.Schema
	found := false
	for _, res := range Module().Resources {
		if s, ok := res.(config.Schema); ok {
			schema, found = s, true
		}
	}
	if !found {
		t.Fatal("the module declares no configuration schema")
	}
	item, ok := schema.Items[rootItem]
	if !ok {
		t.Fatalf("the schema declares no %q item", rootItem)
	}
	if item.Origins != config.OriginPrimary {
		t.Errorf("the %q item accepts origins %b, want the primary source alone (%b)",
			rootItem, item.Origins, config.OriginPrimary)
	}
}

// TestPrepareStatesEnabledForARunnableConfiguration holds that a configuration
// naming a directory lets the module state it runs.
func TestPrepareStatesEnabledForARunnableConfiguration(t *testing.T) {
	reg := registryWithReader(t, bodyFor(filepath.Join(t.TempDir(), "root")))
	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestTheStanceRefusesARootThatNamesNoDirectory holds the premise this backend
// states in Prepare: a root the loader accepted but that names no directory is
// refused with the capability's invalid-config sentinel.
func TestTheStanceRefusesARootThatNamesNoDirectory(t *testing.T) {
	for _, root := range []string{"", "   "} {
		got, err := stance(&moduleConfig{Root: root})
		if !errors.Is(err, objectstore.ErrInvalidConfig) {
			t.Errorf("stance(%q) answered %v, want ErrInvalidConfig", root, err)
		}
		if got != (core.Enablement{}) {
			t.Errorf("stance(%q) = %+v, want the zero stance beside its error", root, got)
		}
	}
	if _, err := stance(&moduleConfig{Root: filepath.Join(t.TempDir(), "root")}); err != nil {
		t.Errorf("stance of a named directory answered %v, want success", err)
	}
}

// TestAMissingRootFailsTheReader drives the real configuration loader: the item
// the backend cannot run without is declared required, so a section that omits
// it is refused when the module reads its own values.
func TestAMissingRootFailsTheReader(t *testing.T) {
	reader, err := loadFrom(t, `{"objectstore.fs":{}}`)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("reading a section with no root gave %v, want one wrapping ErrMissingRequired", err)
	}
}

// TestTheReaderServesACompleteSection is the control for the case above: a
// section naming a root decodes and carries the directory through.
func TestTheReaderServesACompleteSection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	reader, err := loadFrom(t, bodyFor(root))
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		t.Fatalf("reading a complete section: %v", err)
	}
	if got := cfg.resolve().root; got != root {
		t.Errorf("resolved root = %q, want %q", got, root)
	}
}

// TestTheModuleDeliversTheStoreThroughTheRegistry drives a real registry through
// this module's descriptor to Init, resolves the byte seam from the one product
// and names it.
func TestTheModuleDeliversTheStoreThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, bodyFor(filepath.Join(t.TempDir(), "root")))
	reg.Register(Module())

	var seam objectstore.Store
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			seam, err = core.Resolve[objectstore.Store](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if seam == nil || seam.Name() != backendName {
		t.Fatalf("the byte seam resolved %v, want one named %q", seam, backendName)
	}
	if _, ok := seam.(*store); !ok {
		t.Errorf("the resolved store is a %T, not this module's product", seam)
	}
}
