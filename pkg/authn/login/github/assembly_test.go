package github

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// completeBody is the configuration body a working assembly is built from.
func completeBody() string {
	return `{"authn":{"gate":{"mount":{"public-url":"https://app.example"}},` +
		`"login":{"github":{"client-id":"the-id","client-secret":"the-secret"}}}}`
}

// registryWithReader builds a registry with a real configuration reader over a
// body, supplied under the config module's name, which is what a module that
// reads configuration needs before its Prepare runs.
func registryWithReader(t *testing.T, body string) *core.Registry {
	t.Helper()
	reader := authntest.ConfigReaderAll(t, body, Module(), gateStandIn())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg
}

// TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt holds the shape the
// design names: the login method delivered non-exclusively, so several methods
// stand side by side, one schema resource and the optional logging capability.
func TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("Name = %q, want %q", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login method capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethod, not exclusive: several methods stand side by side", p)
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
	got, err := Module().Prepare(t.Context(), registryWithReader(t, completeBody()))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestPrepareRefusesAnUnrunnableConfiguration holds the startup gate: a
// configuration with no public base stops the assembly before anything is built.
func TestPrepareRefusesAnUnrunnableConfiguration(t *testing.T) {
	body := `{"authn":{"login":{"github":{"client-id":"the-id","client-secret":"the-secret"}}}}`
	_, err := Module().Prepare(t.Context(), registryWithReader(t, body))
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("Prepare gave %v, want one wrapping ErrInvalidConfig", err)
	}
}

// TestTheModuleDeliversTheMethodThroughTheRegistry drives a real registry
// through this module's descriptor to Init and resolves the login method from
// the product, naming it.
func TestTheModuleDeliversTheMethodThroughTheRegistry(t *testing.T) {
	reg := registryWithReader(t, completeBody())
	reg.Register(Module())

	var delivered authn.LoginMethod
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, config.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var e error
			delivered, e = core.Resolve[authn.LoginMethod](reg)
			return e
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if delivered == nil {
		t.Fatal("the registry resolved no login method")
	}
	if delivered.Name() != "github" {
		t.Errorf("Name() = %q, want github", delivered.Name())
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess holds that a host that
// imports the package finds the module.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.LoginMethod)(nil)) {
		t.Errorf("the registered module delivers %+v, want the login method capability", got.Provides)
	}
}
