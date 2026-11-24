package sso

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
)

const (
	dbModule      = "stand-in.db"
	mounterModule = "stand-in.mounter"
	admission     = "tenant.admission"
)

func standIn(name string, instance any, exclusive bool, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: exclusive}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

type standInLogger struct{ logs *logBuffer }

func (l standInLogger) Named(string) *slog.Logger { return l.logs.logger() }
func (l standInLogger) Redaction() log.Redaction  { return nil }

func nothing(context.Context, *core.Registry) error { return nil }

type assembly struct {
	keyless       bool
	migrate       bool
	noDatabase    bool
	withAdmission bool
	logging       log.Logger
	methods       []authn.LoginMethod
	loginSwitch   authn.LoginMethodSwitch
}

func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	var items dbtest.Items
	if !a.keyless {
		items = dbtest.Items{"encryption-key": rootKey}
	}
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sso.db"), items)
	if a.migrate {
		handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
		applyMigrations(t, handle, sqliteDialect)
	}
	mounter := mounttest.New(t)
	reader := authntest.ConfigReaderAll(t, "{}", Module())

	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}

	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names = append(names, config.ModuleName)

	if !a.noDatabase {
		reg.Register(standIn(dbModule, database, true, (*db.Database)(nil)))
		names = append(names, dbModule)
	}
	reg.Register(standIn(mounterModule, mounter, true, (*mount.Mounter)(nil)))
	names = append(names, mounterModule)

	if a.withAdmission {
		reg.Register(standIn(admission, struct{}{}, false))
		names = append(names, admission)
	}
	if a.logging != nil {
		reg.Register(standIn("stand-in.log", a.logging, false, (*log.Logger)(nil)))
		names = append(names, "stand-in.log")
	}
	for i, method := range a.methods {
		name := fmt.Sprintf("stand-in.method.%d", i)
		reg.Register(standIn(name, method, false, (*authn.LoginMethod)(nil)))
		names = append(names, name)
	}
	if a.loginSwitch != nil {
		reg.Register(standIn("stand-in.switch", a.loginSwitch, false, (*authn.LoginMethodSwitch)(nil)))
		names = append(names, "stand-in.switch")
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// TestTheDescriptorNamesWhatItDeliversAndRequires checks that the descriptor claims SSOProviders exclusively, requires the database and mounter, and keeps optional capabilities unclaimed.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.sso" {
		t.Errorf("module name = %q (constant %q), want authn.sso", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the SSO provider register alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.SSOProviders)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.SSOProviders exclusively", p)
	}
	var required []core.Token
	for _, r := range m.Requires {
		if !r.Optional {
			required = append(required, r.Token)
		}
	}
	want := []core.Token{(*db.Database)(nil), (*mount.Mounter)(nil)}
	if len(required) != len(want) || required[0] != want[0] || required[1] != want[1] {
		t.Errorf("Requires = %+v, want the database and the mounter", m.Requires)
	}
}

// TestPrepareStatesEnabledAndNothingElse holds the stance.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	reader := authntest.ConfigReaderAll(t, "{}", Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	got, err := Module().Prepare(t.Context(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess holds the init.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.SSOProviders)(nil)) {
		t.Errorf("the registered module delivers %+v, want the SSO provider register", got.Provides)
	}
}

// TestARealRegistryDeliversSSOProviders holds the descriptor end to end.
func TestARealRegistryDeliversSSOProviders(t *testing.T) {
	var providers authn.SSOProviders
	err := assembly{migrate: true, withAdmission: true}.drive(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		providers, err = core.Resolve[authn.SSOProviders](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if providers == nil {
		t.Fatal("the registry delivered nothing for the capability the descriptor claims")
	}
}

// TestTheDriveFailsWithoutADatabase holds the required exclusive dependency.
func TestTheDriveFailsWithoutADatabase(t *testing.T) {
	err := assembly{migrate: true, noDatabase: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no database")
		return nil
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("the drive returned %v, want a missing-provider failure", err)
	}
}

// TestStartRefusesWhenTheTablesAreMissing holds ErrNotMigrated from Start.
func TestStartRefusesWhenTheTablesAreMissing(t *testing.T) {
	err := assembly{withAdmission: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with the tables missing")
		return nil
	})
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestStartRefusesWithoutARootKey holds ErrNoEncryptionKey from Start.
func TestStartRefusesWithoutARootKey(t *testing.T) {
	err := assembly{keyless: true, migrate: true, withAdmission: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no root key")
		return nil
	})
	if !errors.Is(err, authn.ErrNoEncryptionKey) {
		t.Errorf("the drive returned %v, want ErrNoEncryptionKey", err)
	}
}

// TestStartWarnsWhenTheAdmissionLayerTakesNoPart checks that Start warns when the tenant admission layer takes no part.
func TestStartWarnsWhenTheAdmissionLayerTakesNoPart(t *testing.T) {
	logs := &logBuffer{}
	err := assembly{migrate: true, logging: standInLogger{logs: logs}}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got := logs.lines("WARN", admission, "single sign-on"); len(got) == 0 {
		t.Errorf("Start wrote no Warn that %s is not taking part; the log was:\n%s", admission, logs.String())
	}
}

type standInMethod struct{ name string }

func (m standInMethod) Name() string { return m.name }

func (m standInMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{Name: m.name}
}

func (m standInMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.Step{}, errors.New("the stand-in method is never driven")
}

func (m standInMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, errors.New("the stand-in method is never driven")
}

func discoveryUnder(t *testing.T, methods []authn.LoginMethod) map[string][]AuthnSsoProvider {
	t.Helper()
	return discoveryUnderSwitch(t, methods, nil)
}

func discoveryUnderSwitch(t *testing.T, methods []authn.LoginMethod, loginSwitch authn.LoginMethodSwitch) map[string][]AuthnSsoProvider {
	t.Helper()
	answers := map[string][]AuthnSsoProvider{}
	discoveryDriving(t, methods, loginSwitch, func(_ context.Context, _ *product, discover func(string) (discoverResponse, error)) {
		for _, domain := range []string{"oidc.example.com", "saml.example.com"} {
			answer, err := discover(domain)
			if err != nil {
				t.Fatalf("handleDiscover(%s): %v", domain, err)
			}
			answers[domain] = answer.Providers
		}
	})
	return answers
}

func discoveryDriving(t *testing.T, methods []authn.LoginMethod, loginSwitch authn.LoginMethodSwitch,
	run func(ctx context.Context, p *product, discover func(domain string) (discoverResponse, error)),
) {
	t.Helper()
	err := (assembly{migrate: true, methods: methods, loginSwitch: loginSwitch}).drive(t, func(ctx context.Context, reg *core.Registry) error {
		delivered, err := core.Resolve[authn.SSOProviders](reg)
		if err != nil {
			return err
		}
		p, ok := delivered.(*product)
		if !ok {
			return fmt.Errorf("the registry delivered %T, want the module's own product", delivered)
		}
		p.clock = func() time.Time { return base }
		tokens := map[string]string{}
		p.lookup = func(_ context.Context, name string) ([]string, error) {
			if token, ok := tokens[name]; ok {
				return []string{"speed-authn=" + token}, nil
			}
			return nil, nil
		}
		verify := func(protocol, name, domain string) {
			t.Helper()
			cfg := oidcConfig()
			if protocol == authn.SSOProtocolSAML {
				cfg = samlConfig()
			}
			idp, err := p.Put(ctx, authn.SSOProvider{
				Tenant: "tenant-a", Protocol: protocol, Name: name, Enabled: true,
				Provisioning: "jit", UsernameStrategy: "user-confirm", Config: cfg,
			}, nil)
			if err != nil {
				t.Fatalf("Put (%s): %v", protocol, err)
			}
			token, err := p.ClaimDomain(ctx, idp.ID, domain)
			if err != nil {
				t.Fatalf("ClaimDomain (%s): %v", protocol, err)
			}
			tokens[txtName(domain)] = token
			if err := p.VerifyDomain(ctx, idp.ID, domain); err != nil {
				t.Fatalf("VerifyDomain (%s): %v", protocol, err)
			}
		}
		verify(authn.SSOProtocolOIDC, "oidc-idp", "oidc.example.com")
		verify(authn.SSOProtocolSAML, "saml-idp", "saml.example.com")

		run(ctx, p, func(domain string) (discoverResponse, error) {
			return p.handleDiscover(ctx, discoverRequest{Email: "someone@" + domain})
		})
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestTheDiscoveryNamesOnlyTheProtocolTheAssemblyDelivers checks that discovery lists only providers of protocols this run delivers.
func TestTheDiscoveryNamesOnlyTheProtocolTheAssemblyDelivers(t *testing.T) {
	for _, protocol := range []string{authn.SSOProtocolOIDC, authn.SSOProtocolSAML} {
		t.Run(protocol, func(t *testing.T) {
			answers := discoveryUnder(t, []authn.LoginMethod{standInMethod{name: protocol}})
			for _, held := range []struct{ domain, protocol string }{
				{"oidc.example.com", authn.SSOProtocolOIDC},
				{"saml.example.com", authn.SSOProtocolSAML},
			} {
				got := answers[held.domain]
				if held.protocol == protocol {
					if len(got) != 1 || got[0].Protocol != protocol {
						t.Errorf("the discovery of %s listed %+v, want the one %s provider",
							held.domain, got, protocol)
					}
					continue
				}
				if len(got) != 0 {
					t.Errorf("the discovery of %s listed %+v, want none: the protocol is not delivered",
						held.domain, got)
				}
			}
		})
	}
}

// TestTheDiscoveryListsEveryDeliveredProtocol checks that discovery lists providers of every delivered protocol.
func TestTheDiscoveryListsEveryDeliveredProtocol(t *testing.T) {
	answers := discoveryUnder(t, []authn.LoginMethod{
		standInMethod{name: authn.SSOProtocolOIDC},
		standInMethod{name: authn.SSOProtocolSAML},
		standInMethod{name: "password"},
	})
	for _, held := range []struct{ domain, protocol string }{
		{"oidc.example.com", authn.SSOProtocolOIDC},
		{"saml.example.com", authn.SSOProtocolSAML},
	} {
		got := answers[held.domain]
		if len(got) != 1 || got[0].Protocol != held.protocol {
			t.Errorf("the discovery of %s listed %+v, want the one %s provider",
				held.domain, got, held.protocol)
		}
	}
}

// TestTheDiscoveryListsNothingWhenNoMethodIsDelivered checks that discovery lists nothing when no enterprise method is delivered.
func TestTheDiscoveryListsNothingWhenNoMethodIsDelivered(t *testing.T) {
	answers := discoveryUnder(t, nil)
	for _, domain := range []string{"oidc.example.com", "saml.example.com"} {
		if got := answers[domain]; len(got) != 0 {
			t.Errorf("the discovery of %s listed %+v, want none", domain, got)
		}
	}
}

// TestTheDiscoveryLeavesOutAMethodTheRunTimeSwitchDisabled checks that discovery leaves out a method the run-time switch disabled.
func TestTheDiscoveryLeavesOutAMethodTheRunTimeSwitchDisabled(t *testing.T) {
	answers := discoveryUnderSwitch(t, []authn.LoginMethod{
		standInMethod{name: authn.SSOProtocolOIDC},
		standInMethod{name: authn.SSOProtocolSAML},
	}, &standInSwitch{disabled: []string{authn.SSOProtocolOIDC}})

	if got := answers["oidc.example.com"]; len(got) != 0 {
		t.Errorf("the discovery of oidc.example.com listed %+v, want none: the switch turned the method off", got)
	}
	if got := answers["saml.example.com"]; len(got) != 1 || got[0].Protocol != authn.SSOProtocolSAML {
		t.Errorf("the discovery of saml.example.com listed %+v, want the one saml provider", got)
	}
}

// TestTheDiscoveryFollowsTheRunTimeSwitchAsItChanges checks that discovery follows the run-time switch as it changes.
func TestTheDiscoveryFollowsTheRunTimeSwitchAsItChanges(t *testing.T) {
	sw := &standInSwitch{disabled: []string{authn.SSOProtocolOIDC}}
	discoveryDriving(t, []authn.LoginMethod{
		standInMethod{name: authn.SSOProtocolOIDC},
		standInMethod{name: authn.SSOProtocolSAML},
	}, sw, func(_ context.Context, _ *product, discover func(string) (discoverResponse, error)) {
		held := func(domain string) []AuthnSsoProvider {
			t.Helper()
			answer, err := discover(domain)
			if err != nil {
				t.Fatalf("handleDiscover(%s): %v", domain, err)
			}
			return answer.Providers
		}
		if got := held("oidc.example.com"); len(got) != 0 {
			t.Errorf("while oidc was off the discovery listed %+v, want none", got)
		}
		sw.set(nil, nil)
		if got := held("oidc.example.com"); len(got) != 1 || got[0].Protocol != authn.SSOProtocolOIDC {
			t.Errorf("after oidc was turned back on the discovery listed %+v, want the one oidc provider", got)
		}
		sw.set([]string{authn.SSOProtocolOIDC}, nil)
		if got := held("oidc.example.com"); len(got) != 0 {
			t.Errorf("after oidc was turned off again the discovery listed %+v, want none", got)
		}
	})
}

// TestTheDiscoveryListsEveryDeliveredMethodWithoutARunTimeSwitch checks that discovery lists every delivered method when there is no run-time switch.
func TestTheDiscoveryListsEveryDeliveredMethodWithoutARunTimeSwitch(t *testing.T) {
	answers := discoveryUnder(t, []authn.LoginMethod{
		standInMethod{name: authn.SSOProtocolOIDC},
		standInMethod{name: authn.SSOProtocolSAML},
	})
	for _, held := range []struct{ domain, protocol string }{
		{"oidc.example.com", authn.SSOProtocolOIDC},
		{"saml.example.com", authn.SSOProtocolSAML},
	} {
		if got := answers[held.domain]; len(got) != 1 || got[0].Protocol != held.protocol {
			t.Errorf("the discovery of %s listed %+v, want the one %s provider", held.domain, got, held.protocol)
		}
	}
}
