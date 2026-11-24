package saml

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

const (
	providersModule = "stand-in.providers"
	accountsModule  = "stand-in.accounts"
	mounterModule   = "stand-in.mounter"
	configModule    = "stand-in.config"
)

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

type fakeReader struct {
	enabled         bool
	spKeyFile       string
	spCertFile      string
	spNextCertFile  string
	refuseElsewhere bool
}

func (r fakeReader) Decode(path string, target any) error {
	if path != ConfigNamespace {
		if r.refuseElsewhere {
			return fmt.Errorf("the module read %s, which it must not do here", path)
		}
		return nil
	}
	cfg, ok := target.(*moduleConfig)
	if !ok {
		return nil
	}
	cfg.Enabled = r.enabled
	cfg.SPKeyFile = r.spKeyFile
	cfg.SPCertFile = r.spCertFile
	cfg.SPNextCertFile = r.spNextCertFile
	return nil
}

func buildRegistry(t *testing.T, providers *fakeProviders) *core.Registry {
	t.Helper()
	return buildRegistryWith(t, providers, fakeReader{enabled: true})
}

func buildRegistryWith(t *testing.T, providers *fakeProviders, reader config.Reader) *core.Registry {
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
	reg.Supply(reader, configModule)
	return reg
}

func participants() []string {
	return []string{ModuleName, providersModule, accountsModule, mounterModule, configModule}
}

// TestTheDescriptorRequiresTheCapabilitiesAndDeliversBoth checks that the descriptor requires its capabilities and delivers both the method and the refresher.
func TestTheDescriptorRequiresTheCapabilitiesAndDeliversBoth(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.sso.saml" {
		t.Errorf("module name = %q, want authn.sso.saml", m.Name)
	}
	wantReq := []core.Requirement{
		{Token: (*authn.SSOProviders)(nil)},
		{Token: (*authn.Accounts)(nil)},
		{Token: (*mount.Mounter)(nil)},
		{Token: (*log.Logger)(nil), Optional: true},
	}
	if len(m.Requires) != len(wantReq) {
		t.Fatalf("Requires = %+v, want the register, account, mounter and logging capabilities", m.Requires)
	}
	for i, r := range m.Requires {
		if r.Token != wantReq[i].Token || r.Optional != wantReq[i].Optional {
			t.Errorf("Requires[%d] = %+v, want %+v", i, r, wantReq[i])
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

// TestPrepareStandsDownWhenTheSwitchIsOff checks that Prepare stands the method down when the switch is off.
func TestPrepareStandsDownWhenTheSwitchIsOff(t *testing.T) {
	got, err := prepare(fakeReader{refuseElsewhere: true})
	if err != nil {
		t.Fatalf("prepare with the switch off: %v", err)
	}
	if got.State != core.StateDisabled {
		t.Errorf("prepare = %+v, want StateDisabled", got)
	}
	if got.Reason == "" {
		t.Error("the module stood down without a reason")
	}
}

// TestPrepareStatesEnabledWhenTheSwitchIsOn checks that Prepare states enabled when the switch is on.
func TestPrepareStatesEnabledWhenTheSwitchIsOn(t *testing.T) {
	got, err := prepare(fakeReader{enabled: true, refuseElsewhere: true})
	if err != nil {
		t.Fatalf("prepare with the switch on and a legal configuration: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("prepare = %+v, want the enabled stance alone", got)
	}
}

// TestPrepareRefusesAnIncompleteKeyConfigurationWhenTheSwitchIsOn checks that Prepare refuses an incomplete key configuration when the switch is on.
func TestPrepareRefusesAnIncompleteKeyConfigurationWhenTheSwitchIsOn(t *testing.T) {
	_, err := prepare(fakeReader{enabled: true, spKeyFile: "/tmp/sp-key.pem"})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("prepare with a key and no certificate = %v, want authn.ErrInvalidConfig", err)
	}
}

// TestPrepareRefusesAKeyThatIsNotAPEMFileWhenTheSwitchIsOn checks that Prepare refuses a key that is not a PEM file when the switch is on.
func TestPrepareRefusesAKeyThatIsNotAPEMFileWhenTheSwitchIsOn(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "sp-key.pem")
	if err := os.WriteFile(keyFile, []byte("this is not a PEM key\n"), 0o600); err != nil {
		t.Fatalf("writing the key file: %v", err)
	}
	_, err := prepare(fakeReader{
		enabled:    true,
		spKeyFile:  keyFile,
		spCertFile: filepath.Join(dir, "sp-cert.pem"),
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("prepare with a key file that is not a PEM key = %v, want authn.ErrInvalidConfig", err)
	}
}

// TestPrepareDoesNotValidateTheRestWhenTheSwitchIsOff checks that Prepare validates nothing else when the switch is off.
func TestPrepareDoesNotValidateTheRestWhenTheSwitchIsOff(t *testing.T) {
	got, err := prepare(fakeReader{spKeyFile: "/tmp/sp-key.pem"})
	if err != nil {
		t.Fatalf("prepare with the switch off and an incomplete key configuration = %v, want no error: "+
			"the switch is decided before the rest of the section is validated", err)
	}
	if got.State != core.StateDisabled {
		t.Errorf("prepare = %+v, want StateDisabled", got)
	}
	if got.Reason == "" {
		t.Error("the module stood down without a reason")
	}
}

// TestTheRegistryStandsTheMethodDownWhenTheSwitchIsOff checks that the registry stands the method down when the switch is off.
func TestTheRegistryStandsTheMethodDownWhenTheSwitchIsOff(t *testing.T) {
	reg := buildRegistryWith(t, newFakeProviders(providerRow(nil)), fakeReader{})
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: participants(),
		Action: func(_ context.Context, reg *core.Registry) error {
			got, ok := reg.Enablement(ModuleName)
			if !ok {
				t.Fatalf("the registry has no verdict for %s", ModuleName)
			}
			if got.State != core.StateDisabled || got.Reason == "" {
				t.Errorf("the module verdict = %+v, want StateDisabled with a reason", got)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive with the switch off: %v", err)
	}
}

// TestTheRegistryDeliversTheMethodAndTheRefresher checks that the registry delivers the method and the refresher.
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
	if len(methods) != 1 || methods[0].Name() != authn.SSOProtocolSAML {
		t.Fatalf("delivered %v, want one login method named saml", methods)
	}
	if len(refreshers) != 1 || refreshers[0].Protocol() != providerProtocol {
		t.Fatalf("delivered %v, want one metadata refresher for saml", refreshers)
	}
}
