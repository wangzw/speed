package oidc

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The stand-in modules the descriptor names: the enterprise provider register,
// the account module and the configuration reader.
const (
	providersModule = "stand-in.providers"
	accountsModule  = "stand-in.accounts"
	readerModule    = "stand-in.config"
)

// standIn declares a capability the way the real module does.
func standIn(name string, token any) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: token}}}
}

// fakeAccounts is a stand-in for the account module: this method reads no
// account, so every answer is the zero value.
type fakeAccounts struct{}

func (fakeAccounts) Get(context.Context, authn.AccountID) (authn.Account, error) {
	return authn.Account{}, nil
}

func (fakeAccounts) Lookup(context.Context, authn.IdentifierKind, string) (authn.AccountID, error) {
	return "", nil
}

func (fakeAccounts) ByExternal(context.Context, string, string) (authn.AccountID, error) {
	return "", nil
}

func (fakeAccounts) Identifiers(context.Context, authn.AccountID) ([]authn.Identifier, error) {
	return nil, nil
}

func (fakeAccounts) IdentifiersMany(context.Context, []authn.AccountID) (map[authn.AccountID][]authn.Identifier, error) {
	return nil, nil
}

func (fakeAccounts) Externals(context.Context, authn.AccountID) ([]authn.ExternalBinding, error) {
	return nil, nil
}

func (fakeAccounts) List(context.Context, authn.AccountQuery) ([]authn.Account, string, error) {
	return nil, "", nil
}

func (fakeAccounts) ListServiceSecrets(context.Context, authn.AccountID) ([]authn.ServiceSecretInfo, error) {
	return nil, nil
}

// fakeReader is a configuration reader whose sections a case chooses.
type fakeReader struct {
	gm      gateMount
	sso     ssoConfig
	enabled moduleConfig
	err     error
}

func (f *fakeReader) Decode(path string, target any) error {
	if f.err != nil {
		return f.err
	}
	switch path {
	case gateNamespace + "." + gateMountPath:
		if gm, ok := target.(*gateMount); ok {
			*gm = f.gm
		}
	case ssoNamespace:
		if c, ok := target.(*ssoConfig); ok {
			*c = f.sso
		}
	case ConfigNamespace:
		if c, ok := target.(*moduleConfig); ok {
			*c = f.enabled
		}
	}
	return nil
}

// validReader is a reader with a complete configuration and the switch on.
func validReader() *fakeReader {
	return &fakeReader{gm: gateMount{PublicURL: "https://app.example.com"}, enabled: moduleConfig{Enabled: true}}
}

// driveWith registers this module beside the stand-in modules and drives a real
// registry to the stage where the instance exists, handing the registry to
// action.
func driveWith(t *testing.T, reader config.Reader, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(providersModule, (*authn.SSOProviders)(nil)))
	reg.Register(standIn(accountsModule, (*authn.Accounts)(nil)))
	reg.Register(standIn(readerModule, (*config.Reader)(nil)))
	reg.Supply(newFakeProviders(), providersModule)
	reg.Supply(fakeAccounts{}, accountsModule)
	if reader != nil {
		reg.Supply(reader, readerModule)
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, providersModule, accountsModule, readerModule},
		Action:       action,
	})
}

// TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt holds the descriptor
// shape: the login method capability delivered non-exclusively, the provider
// register and the account module required, and the settings store and logging
// capability optional.
func TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.sso.oidc" {
		t.Errorf("module name = %q (constant %q), want authn.sso.oidc", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login method capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethod, not exclusive", p)
	}
	required := map[core.Token]bool{}
	for _, req := range m.Requires {
		if req.Optional {
			continue
		}
		required[req.Token] = true
	}
	if !required[core.Token((*authn.SSOProviders)(nil))] || !required[core.Token((*authn.Accounts)(nil))] {
		t.Errorf("required capabilities = %+v, want the provider register and the account module", m.Requires)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the one enable-switch schema", m.Resources)
	}
}

// TestPrepareStandsDownWhenTheSwitchIsOff holds the unconfigured case: the
// method has nothing of its own to infer the intent from, so an absent switch
// means the deployment did not offer enterprise sign-in; the module is absent
// from the run without failing the startup.
func TestPrepareStandsDownWhenTheSwitchIsOff(t *testing.T) {
	err := driveWith(t, &fakeReader{}, func(_ context.Context, reg *core.Registry) error {
		got, ok := reg.Enablement(ModuleName)
		if !ok {
			t.Fatalf("the registry has no verdict for %s", ModuleName)
		}
		if got.State != core.StateDisabled {
			t.Errorf("the module state = %v, want StateDisabled", got.State)
		}
		if got.Reason == "" {
			t.Error("the module stood down without a reason")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("drive with the switch off: %v", err)
	}
}

// TestPrepareStatesEnabledAndRefusesAnIncompleteConfiguration holds that a
// complete configuration enables the module and a missing public address fails
// the start once the switch is on.
func TestPrepareStatesEnabledAndRefusesAnIncompleteConfiguration(t *testing.T) {
	if err := driveWith(t, validReader(), func(context.Context, *core.Registry) error { return nil }); err != nil {
		t.Fatalf("drive with a complete configuration: %v", err)
	}
	err := driveWith(t, &fakeReader{enabled: moduleConfig{Enabled: true}}, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no public address")
		return nil
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("drive error = %v, want authn.ErrInvalidConfig", err)
	}
}

// TestARealRegistryDeliversTheOIDCMethod holds that the method is delivered and
// wired to the configuration the registry resolved.
func TestARealRegistryDeliversTheOIDCMethod(t *testing.T) {
	var methods []authn.LoginMethod
	err := driveWith(t, validReader(), func(_ context.Context, reg *core.Registry) error {
		methods = core.ResolveAll[authn.LoginMethod](reg)
		return nil
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != authn.SSOProtocolOIDC {
		t.Fatalf("delivered %v, want one login method named oidc", methods)
	}
	p, ok := methods[0].(*product)
	if !ok {
		t.Fatalf("the delivered method is %T, not this module's product", methods[0])
	}
	if p.redirect.callback("idp-1") != "https://app.example.com/api/v1/auth/callback/idp-1" {
		t.Errorf("redirect = %q, want the resolved public address and callback path", p.redirect.callback("idp-1"))
	}
}

// TestOptionalCapabilitiesAbsentIsALegalAssembly holds that an assembly with no
// settings store, no logging, no audit, no event bus and no metric capability
// still builds.
func TestOptionalCapabilitiesAbsentIsALegalAssembly(t *testing.T) {
	if err := driveWith(t, validReader(), func(context.Context, *core.Registry) error { return nil }); err != nil {
		t.Fatalf("drive with no optional capability: %v", err)
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

// TestTheSettingsReaderFallsBackToTheDeclaredDefault holds that a reader with no
// store answers the declared default.
func TestTheSettingsReaderFallsBackToTheDeclaredDefault(t *testing.T) {
	r := newReader(nil, nil)
	if got := r.providerCacheTTL(); got != defaultProviderCacheTTL {
		t.Errorf("providerCacheTTL = %v, want %v", got, defaultProviderCacheTTL)
	}
	if got := r.egressTimeout(); got != defaultEgressTimeout {
		t.Errorf("egressTimeout = %v, want %v", got, defaultEgressTimeout)
	}
}
