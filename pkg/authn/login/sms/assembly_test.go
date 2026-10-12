package sms

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The stand-ins a driven registry is built from: one module that delivers the
// three capabilities this method uses, each exclusively as its own module does,
// and one that delivers the configuration reader.
const (
	smsModule    = "stand-in.sms"
	configModule = "stand-in.config"
)

// standInSMS declares the three capabilities the way the SMS module and the
// account module do.
func standInSMS() core.Module {
	return core.Module{
		Name: smsModule,
		Provides: []core.Provision{
			{Token: (*authn.SMSCodes)(nil), Exclusive: true},
			{Token: (*authn.Throttle)(nil), Exclusive: true},
			{Token: (*authn.Accounts)(nil), Exclusive: true},
		},
	}
}

// standInConfig declares the configuration reader the way the config module
// does.
func standInConfig() core.Module {
	return core.Module{
		Name:     configModule,
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	}
}

// provider is one instance that is all three capabilities, as one delivery of
// each is what a real assembly has.
type provider struct {
	*fakeSMSCodes
	*fakeThrottle
	*fakeAccounts
}

func newProvider() *provider {
	return &provider{
		fakeSMSCodes: &fakeSMSCodes{sendChallenge: challengeID},
		fakeThrottle: &fakeThrottle{},
		fakeAccounts: &fakeAccounts{},
	}
}

// fakeReader is a configuration reader whose account switches a case chooses.
type fakeReader struct {
	signup string
	err    error
}

func (f *fakeReader) Decode(path string, target any) error {
	if f.err != nil {
		return f.err
	}
	if path == "authn.account" {
		if c, ok := target.(*accountConfig); ok {
			c.Signup = f.signup
		}
	}
	return nil
}

// buildRegistry registers this module beside the stand-ins and supplies the
// provider and reader under their modules.
func buildRegistry(reader config.Reader) (*core.Registry, *provider) {
	p := newProvider()
	reg := core.New()
	reg.Register(Module())
	reg.Register(standInSMS())
	reg.Register(standInConfig())
	reg.Supply(p, smsModule)
	reg.Supply(reader, configModule)
	return reg, p
}

func TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != "authn.login.sms" || ModuleName != "authn.login.sms" {
		t.Errorf("module name = %q (constant %q), want authn.login.sms", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login method capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethod, not exclusive: several methods stand side by side", p)
	}
	want := []core.Token{(*authn.SMSCodes)(nil), (*authn.Throttle)(nil), (*authn.Accounts)(nil)}
	if len(m.Requires) != len(want) {
		t.Fatalf("Requires = %+v, want the SMS, back-off and account capabilities", m.Requires)
	}
	for i, r := range m.Requires {
		if r.Token != want[i] || r.Optional {
			t.Errorf("Requires[%d] = %+v, want %v and not optional: an SMS method with no such module cannot run", i, r, want[i])
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

func TestARealRegistryDeliversTheSMSMethodOnTheOtherModulesCapabilities(t *testing.T) {
	reg, p := buildRegistry(&fakeReader{})
	var methods []authn.LoginMethod
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, smsModule, configModule},
		Action: func(_ context.Context, reg *core.Registry) error {
			methods = core.ResolveAll[authn.LoginMethod](reg)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != "sms" {
		t.Fatalf("delivered %v, want one login method named sms", methods)
	}
	// The instance is wired to the capabilities the registry resolved, not to
	// nothing and not to a copy: a registered number goes through the fakes.
	p.lookupID = testAccount
	p.accounts = map[authn.AccountID]authn.Account{testAccount: {ID: testAccount, Status: authn.StatusActive}}
	got, _ := methods[0].Begin(t.Context(), loginStart(phoneFields(phoneNumber)))
	if step.KindOf(got) != step.KindPrompt || step.NameOf(got) != stepCode || step.SendsOf(got) != 1 {
		t.Fatalf("Begin answered %s, want a code prompt reporting one send", describe(got))
	}
	if len(p.sends) != 1 || !p.sends[0].Deliver {
		t.Fatalf("the fakes saw %+v, want one delivered send", p.sends)
	}
}

// TestTheRegistrationSwitchIsReadFromTheConfiguration checks that an unknown
// number is delivered only while the account module's registration switch reads
// open, and that any other value, or an unreadable one, reads as closed.
func TestTheRegistrationSwitchIsReadFromTheConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		reader config.Reader
		want   bool
	}{
		{"open", &fakeReader{signup: "open"}, true},
		{"closed", &fakeReader{signup: "closed"}, false},
		{"neither", &fakeReader{signup: "maybe"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg, p := buildRegistry(tc.reader)
			err := reg.Drive(t.Context(), core.Drive{
				Depth:        core.StageInit,
				Participants: []string{ModuleName, smsModule, configModule},
				Action: func(_ context.Context, reg *core.Registry) error {
					m := core.ResolveAll[authn.LoginMethod](reg)[0]
					if got, _ := m.Begin(t.Context(), loginStart(phoneFields(unknownPhone))); step.KindOf(got) != step.KindPrompt {
						t.Fatalf("Begin = %s, want a prompt", describe(got))
					}
					return nil
				},
			})
			if err != nil {
				t.Fatalf("drive: %v", err)
			}
			if len(p.sends) != 1 {
				t.Fatalf("the fake saw %d sends, want one", len(p.sends))
			}
			if got := p.sends[0].Deliver; got != tc.want {
				t.Errorf("an unknown number was delivered = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAnAbsentConfigurationReaderClosesTheRegistration checks that a registry
// with no configuration reader still builds the method, and that an unknown
// number is then suppressed.
func TestAnAbsentConfigurationReaderClosesTheRegistration(t *testing.T) {
	p := newProvider()
	reg := core.New()
	reg.Register(Module())
	reg.Register(standInSMS())
	reg.Supply(p, smsModule)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, smsModule},
		Action: func(_ context.Context, reg *core.Registry) error {
			m := core.ResolveAll[authn.LoginMethod](reg)[0]
			if got, _ := m.Begin(t.Context(), loginStart(phoneFields(unknownPhone))); step.KindOf(got) != step.KindPrompt {
				t.Fatalf("Begin = %s, want a prompt", describe(got))
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(p.sends) != 1 || p.sends[0].Deliver {
		t.Fatalf("the fake saw %+v, want one suppressed send: no reader closes registration", p.sends)
	}
}

func TestACommandWithoutTheSMSOrAccountModulesFailsBeforeAnythingIsBuilt(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName},
		Action: func(context.Context, *core.Registry) error {
			t.Error("the drive reached its action with no SMS or account module")
			return nil
		},
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("drive error = %v, want core.ErrMissingProvider", err)
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
