package saml

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// The stand-in modules a driven registry is built from, each delivering one of
// the capabilities this method requires.
const (
	providersModule = "stand-in.providers"
	accountsModule  = "stand-in.accounts"
	mounterModule   = "stand-in.mounter"
	configModule    = "stand-in.config"
)

// accountsStandIn satisfies authn.Accounts without being called: the SAML method
// requires the account reads for the assembly to be complete, and does not read
// through them itself.
type accountsStandIn struct{ authn.Accounts }

func providersStandIn() core.Module {
	return core.Module{
		Name:     providersModule,
		Provides: []core.Provision{{Token: (*authn.SSOProviders)(nil), Exclusive: true}},
	}
}

func accountsModuleDef() core.Module {
	return core.Module{
		Name:     accountsModule,
		Provides: []core.Provision{{Token: (*authn.Accounts)(nil), Exclusive: true}},
	}
}

func mounterModuleDef() core.Module {
	return core.Module{
		Name:     mounterModule,
		Provides: []core.Provision{{Token: (*mount.Mounter)(nil), Exclusive: true}},
	}
}

func configModuleDef() core.Module {
	return core.Module{
		Name:     configModule,
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	}
}

// fakeReader answers the two namespaces this module reads with their zero
// values, which is a deployment with no service provider key.
type fakeReader struct{}

func (fakeReader) Decode(string, any) error { return nil }

func buildRegistry(t *testing.T, providers *fakeProviders) *core.Registry {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	reg.Register(providersStandIn())
	reg.Register(accountsModuleDef())
	reg.Register(mounterModuleDef())
	reg.Register(configModuleDef())
	reg.Supply(providers, providersModule)
	reg.Supply(accountsStandIn{}, accountsModule)
	reg.Supply(mounttest.New(t, mounttest.WithPublicURL(testPublicURL), mounttest.WithBase(testBasePath)), mounterModule)
	reg.Supply(fakeReader{}, configModule)
	return reg
}

func participants() []string {
	return []string{ModuleName, providersModule, accountsModule, mounterModule, configModule}
}

func TestTheDescriptorRequiresTheThreeAndDeliversBoth(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.sso.saml" {
		t.Errorf("module name = %q, want authn.sso.saml", m.Name)
	}
	wantReq := []core.Token{(*authn.SSOProviders)(nil), (*authn.Accounts)(nil), (*mount.Mounter)(nil)}
	if len(m.Requires) != len(wantReq) {
		t.Fatalf("Requires = %+v, want the register, account and mounter capabilities", m.Requires)
	}
	for i, r := range m.Requires {
		if r.Token != wantReq[i] || r.Optional {
			t.Errorf("Requires[%d] = %+v, want %v, not optional", i, r, wantReq[i])
		}
	}
	wantProv := []core.Token{(*authn.LoginMethod)(nil), (*authn.SSOMetadataRefresher)(nil)}
	if len(m.Provides) != len(wantProv) {
		t.Fatalf("Provides = %+v, want the login method and the metadata refresher", m.Provides)
	}
	for i, p := range m.Provides {
		if p.Token != wantProv[i] || p.Exclusive {
			t.Errorf("Provides[%d] = %+v, want %v, not exclusive: several stand side by side", i, p, wantProv[i])
		}
	}
}

func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	got, err := Module().Prepare(context.Background(), core.New())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

func TestTheRegistryDeliversTheMethodAndTheRefresher(t *testing.T) {
	idp := newIDP(t)
	providers := newFakeProviders(providerRow(providerConfigJSON(t, idp, nil)))
	reg := buildRegistry(t, providers)
	var methods []authn.LoginMethod
	var refreshers []authn.SSOMetadataRefresher
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants(),
		Action: func(_ context.Context, reg *core.Registry) error {
			methods = core.ResolveAll[authn.LoginMethod](reg)
			refreshers = core.ResolveAll[authn.SSOMetadataRefresher](reg)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != methodName {
		t.Fatalf("delivered %v, want one login method named saml", methods)
	}
	if len(refreshers) != 1 || refreshers[0].Protocol() != providerProtocol {
		t.Fatalf("delivered %v, want one metadata refresher for saml", refreshers)
	}
}
