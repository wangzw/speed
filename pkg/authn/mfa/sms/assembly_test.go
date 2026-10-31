package sms

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/configtest"
)

// The stand-in modules an assembly drives: the SMS code engine and the account
// reads, each delivered exclusively by another module of the directory in a real
// command, supplied to a module of its own here.
const (
	smsModule     = "stand-in.sms"
	accountModule = "stand-in.account"
)

// standIn is a module that declares the token exclusively and delivers instance
// from its New.
func standIn(name string, instance any, token core.Token) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// driveWith runs a real registry through this module's descriptor to the stage
// where the instance exists and its dependencies were resolved, and hands the
// registry to action. A nil codes or accounts leaves that stand-in out, which is
// how the missing-dependency cases are driven.
func driveWith(t *testing.T, codes *fakeCodes, accounts *fakeAccounts, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}
	if codes != nil {
		reg.Register(standIn(smsModule, codes, (*authn.SMSCodes)(nil)))
		names = append(names, smsModule)
	}
	if accounts != nil {
		reg.Register(standIn(accountModule, accounts, (*authn.Accounts)(nil)))
		names = append(names, accountModule)
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action:       action,
	})
}

func TestTheDescriptorDeliversTheFactorWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != "authn.mfa.sms" || ModuleName != "authn.mfa.sms" {
		t.Errorf("module name = %q (constant %q), want authn.mfa.sms", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the factor capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.Factor)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.Factor, not exclusive: one module delivers each kind and several kinds stand side by side", p)
	}
	want := []core.Token{(*authn.SMSCodes)(nil), (*authn.Accounts)(nil)}
	if len(m.Requires) != len(want) {
		t.Fatalf("Requires = %+v, want the SMS code engine and the account reads", m.Requires)
	}
	for i, r := range m.Requires {
		if r.Token != want[i] || r.Optional {
			t.Errorf("Requires[%d] = %+v, want %v and not optional: a factor with no engine or no account store cannot run", i, r, want[i])
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

// TestPrepareStandsDownWhenNeitherCarrierNorTemplateIsConfigured holds the
// all-empty case: with no carrier selected and no template configured, SMS was
// not configured for this run and the second factor is not offered.
func TestPrepareStandsDownWhenNeitherCarrierNorTemplateIsConfigured(t *testing.T) {
	reader := configtest.Reader(t, "{}", Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got.State != core.StateDisabled || !strings.Contains(got.Reason, "sms.registry.templates") {
		t.Errorf("Prepare = %+v, want disabled and naming the template set", got)
	}
}

func TestARealRegistryDeliversTheSMSFactorOnTheEngineAndAccountReads(t *testing.T) {
	codes := &fakeCodes{sendID: sentinelChallenge}
	accounts := &fakeAccounts{identifiers: []authn.Identifier{provenPhoneIdentifier()}}
	var factors []authn.Factor
	err := driveWith(t, codes, accounts, func(ctx context.Context, reg *core.Registry) error {
		factors = core.ResolveAll[authn.Factor](reg)
		if len(factors) != 1 {
			t.Fatalf("the registry delivered %d factors, want one", len(factors))
		}
		if factors[0].Kind() != authn.FactorSMS {
			t.Fatalf("the delivered factor is of kind %q, want %q", factors[0].Kind(), authn.FactorSMS)
		}
		// The instance is wired to the capabilities the registry resolved, not to
		// nothing: a prove-phone enrollment goes through the supplied account
		// reads, and a challenge sends through the supplied code engine.
		enrolled, err := factors[0].Enroll(ctx, flow(0, "", nil), submit(nil))
		if err != nil || step.KindOf(enrolled) != step.KindEnrolled {
			t.Fatalf("Enroll answered %s (error %v), want an enrollment", step.KindOf(enrolled), err)
		}
		challenged, err := factors[0].Challenge(ctx, flow(1, "", nil), record())
		if err != nil || step.KindOf(challenged) != step.KindPrompt {
			t.Fatalf("Challenge answered %s (error %v), want a prompt", step.KindOf(challenged), err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if accounts.identifierCalls() != 2 {
		t.Errorf("the account reads were asked %d times, want 2: both the enrollment and the challenge read the phone", accounts.identifierCalls())
	}
	if codes.sendCount() == 0 {
		t.Error("the code engine was never asked to send: the factor is not wired to it")
	}
}

func TestACommandWithoutTheSMSCodeEngineFailsBeforeAnythingIsBuilt(t *testing.T) {
	err := driveWith(t, nil, &fakeAccounts{}, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no SMS code engine")
		return nil
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("drive error = %v, want core.ErrMissingProvider", err)
	}
}

func TestACommandWithoutTheAccountReadsFailsToo(t *testing.T) {
	err := driveWith(t, &fakeCodes{}, nil, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no account reads")
		return nil
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("drive error = %v, want core.ErrMissingProvider: a factor that cannot read the phone must not run", err)
	}
}

func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s: a host that imports the package would not find it", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.Factor)(nil)) {
		t.Errorf("the registered module delivers %+v, want the factor capability", got.Provides)
	}
}
