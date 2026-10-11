package password

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/core"
)

// accountModule is the stand-in for the account module: the one module that
// delivers the password and back-off capabilities, each exclusively. The
// instance a case supplies is a fake that records what it was asked.
const accountModule = "stand-in.account"

// standInAccount declares the two capabilities the way the account module does.
func standInAccount() core.Module {
	return core.Module{
		Name: accountModule,
		Provides: []core.Provision{
			{Token: (*authn.Passwords)(nil), Exclusive: true},
			{Token: (*authn.Throttle)(nil), Exclusive: true},
		},
	}
}

// account is one instance that is both capabilities, as the account module's is.
type account struct {
	*fakePasswords
	*fakeThrottle
}

// driveWith runs a real registry through this module's descriptor to the stage
// where the instance exists and its dependencies were resolved, and hands the
// registry to action.
func driveWith(t *testing.T, supply any, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}
	if supply != nil {
		reg.Register(standInAccount())
		reg.Supply(supply, accountModule)
		names = append(names, accountModule)
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action:       action,
	})
}

func TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != "authn.login.password" || ModuleName != "authn.login.password" {
		t.Errorf("module name = %q (constant %q), want authn.login.password", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login method capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethod, not exclusive: several methods stand side by side", p)
	}
	want := []core.Token{(*authn.Passwords)(nil), (*authn.Throttle)(nil)}
	if len(m.Requires) != len(want) {
		t.Fatalf("Requires = %+v, want the password and back-off capabilities", m.Requires)
	}
	for i, r := range m.Requires {
		if r.Token != want[i] || r.Optional {
			t.Errorf("Requires[%d] = %+v, want %v and not optional: a password method with no account module cannot run", i, r, want[i])
		}
	}
	if len(m.Resources) != 0 {
		t.Errorf("Resources = %v, want none: the module declares no input item", m.Resources)
	}
}

func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	got, err := Module().Prepare(t.Context(), core.New())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

func TestARealRegistryDeliversThePasswordMethodOnTheAccountModulesCapabilities(t *testing.T) {
	l := &ledger{}
	th := &fakeThrottle{led: l}
	pw := &fakePasswords{led: l}
	pw.verify = newRig().pw.verify
	var methods []authn.LoginMethod
	err := driveWith(t, account{pw, th}, func(_ context.Context, reg *core.Registry) error {
		methods = core.ResolveAll[authn.LoginMethod](reg)
		return nil
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != "password" {
		t.Fatalf("delivered %v, want one login method named password", methods)
	}
	// The instance is wired to the capabilities the registry resolved, not to
	// nothing and not to a copy: a credential that holds goes through the fakes.
	got, err := methods[0].Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword)))
	if err != nil || step.KindOf(got) != step.KindProven {
		t.Fatalf("Begin answered %s (error %v), want a proof", describe(got), err)
	}
	if ops := l.ops(); len(ops) != 3 || ops[0] != "check" || ops[1] != "verify" || ops[2] != "succeed" {
		t.Errorf("the fakes saw %v, want a check, a derivation and a settlement", ops)
	}
}

func TestACommandWithoutAnAccountModuleFailsBeforeAnythingIsBuilt(t *testing.T) {
	err := driveWith(t, nil, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no account module")
		return nil
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("drive error = %v, want core.ErrMissingProvider", err)
	}
}

func TestACommandWhoseAccountModuleLacksTheBackOffFailsToo(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     accountModule,
		Provides: []core.Provision{{Token: (*authn.Passwords)(nil), Exclusive: true}},
	})
	reg.Supply(&fakePasswords{led: &ledger{}}, accountModule)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, accountModule},
		Action: func(context.Context, *core.Registry) error {
			t.Error("the drive reached its action with no back-off capability")
			return nil
		},
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("drive error = %v, want core.ErrMissingProvider: a method that cannot count failures must not run uncounted", err)
	}
}

func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s: a host that imports the package would not find it", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.LoginMethod)(nil)) {
		t.Errorf("the registered module delivers %+v, want the login method capability", got.Provides)
	}
}
