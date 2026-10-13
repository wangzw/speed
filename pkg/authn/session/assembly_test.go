package session

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// The names of the stand-in modules that provide what this module requires.
const (
	dbModule       = "stand-in.db"
	mountModule    = "stand-in.mount"
	accountsModule = "stand-in.accounts"
)

// standIn declares the capabilities a stand-in provides, each without a New: the
// instance is supplied by the case.
func standIn(name string, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, token := range tokens {
		provides[i] = core.Provision{Token: token}
	}
	return core.Module{Name: name, Provides: provides}
}

// driveRegister builds a real registry with this module and stand-ins for the
// three capabilities it requires, a real database among them, and drives it to
// the stage where every instance exists and its dependencies were resolved.
func driveRegister(t *testing.T, action func(context.Context, *core.Registry) error) error {
	t.Helper()
	d := dialects[0]
	database := d.open(t, d.newDSN(t))
	mounter := mounttest.New(t)
	accounts := newFakeAccounts()
	reader := authntest.ConfigReaderAll(t,
		`{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}}}}`, Module(), gate.Module())

	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(dbModule, (*db.Database)(nil)))
	reg.Register(standIn(mountModule, (*mount.Mounter)(nil)))
	reg.Register(standIn(accountsModule, (*authn.Accounts)(nil)))
	reg.Register(config.Module())
	reg.Supply(database, dbModule)
	reg.Supply(mounter, mountModule)
	reg.Supply(accounts, accountsModule)
	reg.Supply(reader, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, dbModule, mountModule, accountsModule, config.ModuleName},
		Action:       action,
	})
}

// The three capabilities the descriptor declares resolve to the one product the
// module built: the delivery check (assertDelivered) runs as the instance is
// registered, so a product that did not implement one of them would fail the
// drive before this case's action ran.
func TestTheModuleDeliversItsThreeCapabilities(t *testing.T) {
	var sessions authn.Sessions
	var verifier authn.Verifier
	var lifecycle authn.AccountLifecycle
	err := driveRegister(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		if sessions, err = core.Resolve[authn.Sessions](reg); err != nil {
			return err
		}
		if verifier, err = core.Resolve[authn.Verifier](reg); err != nil {
			return err
		}
		if lifecycle, err = core.Resolve[authn.AccountLifecycle](reg); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if sessions == nil || verifier == nil || lifecycle == nil {
		t.Fatalf("a capability resolved to nil: Sessions %v, Verifier %v, AccountLifecycle %v", sessions, verifier, lifecycle)
	}
	// One instance satisfies all three, as the module's design says.
	if sessions != verifier.(authn.Sessions) {
		t.Errorf("the session store and the verifying backend are different instances")
	}
	if verifier.Name() != backendName {
		t.Errorf("the backend name is %q, want %q", verifier.Name(), backendName)
	}
	if a := verifier.Accepts(); a.Impersonation != true || a.SSO != true || a.Accounts != "" ||
		len(a.Cookies) != 1 || a.Cookies[0] != hostCookieName || len(a.Schemes) != 0 {
		t.Errorf("Accepts = %+v, want the session cookie, the library namespace, impersonation and SSO", a)
	}
}

// The descriptor states what the design states: three required capabilities and
// one optional (the audit seam, taken up in Init), three provisions with the
// session store exclusive, and the enabled stance.
func TestTheDescriptorStatesWhatTheDesignStates(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.session" {
		t.Errorf("the module is named %q (constant %q), want authn.session", m.Name, ModuleName)
	}
	wantRequired := []core.Token{(*db.Database)(nil), (*mount.Mounter)(nil), (*authn.Accounts)(nil)}
	wantOptional := []core.Token{(*audit.Audit)(nil)}
	if len(m.Requires) != len(wantRequired)+len(wantOptional) {
		t.Fatalf("Requires = %+v, want three required and one optional", m.Requires)
	}
	for i, want := range wantRequired {
		if m.Requires[i].Token != want || m.Requires[i].Optional {
			t.Errorf("Requires[%d] = %+v, want %v not optional", i, m.Requires[i], want)
		}
	}
	for i, want := range wantOptional {
		if got := m.Requires[len(wantRequired)+i]; got.Token != want || !got.Optional {
			t.Errorf("Requires[%d] = %+v, want %v optional", len(wantRequired)+i, got, want)
		}
	}
	wantProvides := []struct {
		token     core.Token
		exclusive bool
	}{
		{(*authn.Sessions)(nil), true},
		{(*authn.Verifier)(nil), false},
		{(*authn.AccountLifecycle)(nil), false},
	}
	if len(m.Provides) != len(wantProvides) {
		t.Fatalf("Provides = %+v, want three", m.Provides)
	}
	for i, want := range wantProvides {
		if m.Provides[i].Token != want.token || m.Provides[i].Exclusive != want.exclusive {
			t.Errorf("Provides[%d] = %+v, want %v exclusive=%v", i, m.Provides[i], want.token, want.exclusive)
		}
	}
}

// Importing the package is what puts the module in the process registry.
func TestTheProcessRegistryCarriesTheModule(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 3 {
		t.Errorf("the registered module delivers %+v, want three capabilities", got.Provides)
	}
}

// The instance also satisfies the optional CookieClearer, which is not a declared
// provision: the gateway discovers it by a type assertion on the backend.
func TestTheBackendAlsoClearsCookies(t *testing.T) {
	var verifier authn.Verifier
	err := driveRegister(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		verifier, err = core.Resolve[authn.Verifier](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if _, ok := verifier.(authn.CookieClearer); !ok {
		t.Errorf("the backend %T does not implement CookieClearer", verifier)
	}
}

// A command without one of the required capabilities fails at resolution, naming
// it, before anything is built.
func TestACommandWithoutARequiredCapabilityFails(t *testing.T) {
	d := dialects[0]
	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(dbModule, (*db.Database)(nil)))
	reg.Register(standIn(mountModule, (*mount.Mounter)(nil)))
	// No accounts provider.
	reg.Register(config.Module())
	reg.Supply(d.open(t, d.newDSN(t)), dbModule)
	reg.Supply(mounttest.New(t), mountModule)
	reg.Supply(authntest.ConfigReaderAll(t,
		`{"authn":{"gate":{"mount":{"public-url":"https://app.example.com"}}}}`, Module(), gate.Module()), config.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, dbModule, mountModule, config.ModuleName},
		Action: func(context.Context, *core.Registry) error {
			t.Error("the drive reached its action with no accounts provider")
			return nil
		},
	})
	if err == nil {
		t.Fatal("the drive succeeded with no accounts provider")
	}
}
