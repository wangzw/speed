package password

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/core"
)

const accountModule = "stand-in.account"

func standInAccount() core.Module {
	return core.Module{
		Name: accountModule,
		Provides: []core.Provision{
			{Token: (*authn.Passwords)(nil), Exclusive: true},
			{Token: (*authn.Throttle)(nil), Exclusive: true},
		},
	}
}

type account struct {
	*fakePasswords
	*fakeThrottle
}

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

// TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt checks that the method is non-exclusive and requires the password and back-off capabilities.
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

// TestPrepareStatesEnabledAndNothingElse checks that Prepare reports the module
// enabled and nothing else.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	got, err := Module().Prepare(t.Context(), core.New())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestARealRegistryDeliversThePasswordMethodOnTheAccountModulesCapabilities checks that a real registry supplies and runs the method.
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
	got, err := methods[0].Begin(t.Context(), loginStart(credentialFields("alice", sentinelPassword)))
	if err != nil || step.KindOf(got) != step.KindProven {
		t.Fatalf("Begin answered %s (error %v), want a proof", describe(got), err)
	}
	if ops := l.ops(); len(ops) != 3 || ops[0] != "check" || ops[1] != "verify" || ops[2] != "succeed" {
		t.Errorf("the fakes saw %v, want a check, a derivation and a settlement", ops)
	}
}

// TestACommandWithoutAnAccountModuleFailsBeforeAnythingIsBuilt checks the missing-provider failure before the action runs.
func TestACommandWithoutAnAccountModuleFailsBeforeAnythingIsBuilt(t *testing.T) {
	err := driveWith(t, nil, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no account module")
		return nil
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("drive error = %v, want core.ErrMissingProvider", err)
	}
}

// TestACommandWhoseAccountModuleLacksTheBackOffFailsToo checks that a missing back-off capability also fails as a missing provider.
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

// TestImportingThePackageRegistersTheModuleWithTheProcess checks that importing
// the package registers the module with the process registry.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s: a host that imports the package would not find it", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.LoginMethod)(nil)) {
		t.Errorf("the registered module delivers %+v, want the login method capability", got.Provides)
	}
}
