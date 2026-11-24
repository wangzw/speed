package google

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

const (
	configModule = "stand-in.config"
	logModule    = "stand-in.log"
)

func standInLog() core.Module {
	return core.Module{
		Name:     logModule,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
	}
}

type recordingLogger struct {
	patterns map[string]log.Matcher
}

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{patterns: map[string]log.Matcher{}}
}

func (l *recordingLogger) Named(string) *slog.Logger             { return slog.Default() }
func (l *recordingLogger) Redaction() log.Redaction              { return l }
func (l *recordingLogger) AddKeys(...string)                     {}
func (l *recordingLogger) AddPattern(name string, m log.Matcher) { l.patterns[name] = m }

func standInConfig() core.Module {
	return core.Module{
		Name:     configModule,
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	}
}

type fakeReader struct {
	cfg moduleConfig
	gm  gateMount
	err error
}

func (f *fakeReader) Decode(path string, target any) error {
	if f.err != nil {
		return f.err
	}
	switch path {
	case ConfigNamespace:
		switch t := target.(type) {
		case *moduleConfig:
			*t = f.cfg
		case *clientIDOnly:
			if f.cfg.ClientID == "" {
				return config.ErrMissingRequired
			}
			t.ClientID = f.cfg.ClientID
		case *clientSecretOnly:
			if f.cfg.ClientSecret == "" {
				return config.ErrMissingRequired
			}
			t.ClientSecret = f.cfg.ClientSecret
		}
	case gateNamespace + "." + gateMountPath:
		if gm, ok := target.(*gateMount); ok {
			*gm = f.gm
		}
	}
	return nil
}

func validReader() *fakeReader {
	return &fakeReader{
		cfg: moduleConfig{ClientID: testClientID, ClientSecret: testClientSecret},
		gm:  gateMount{PublicURL: "https://app.example.com"},
	}
}

func driveWith(t *testing.T, reader config.Reader, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	reg.Register(standInConfig())
	if reader != nil {
		reg.Supply(reader, configModule)
	}
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, configModule},
		Action:       action,
	})
}

// TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt holds that the method is delivered non-exclusively and requires nothing of another module.
func TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != "authn.login.google" || ModuleName != "authn.login.google" {
		t.Errorf("module name = %q (constant %q), want authn.login.google", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login method capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethod, not exclusive: several methods stand side by side", p)
	}
	if len(m.Requires) != 1 || m.Requires[0].Token != core.Token((*log.Logger)(nil)) || !m.Requires[0].Optional {
		t.Errorf("Requires = %+v, want the logging capability alone and optional: the provider is reached over the "+
			"client this module builds, and the secret is registered with logging when there is any", m.Requires)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the config schema alone", m.Resources)
	}
}

// TestPrepareStatesEnabledAndRefusesAnIncompleteConfiguration holds that a complete configuration enables and an incomplete one fails the start.
func TestPrepareStatesEnabledAndRefusesAnIncompleteConfiguration(t *testing.T) {
	if err := driveWith(t, validReader(), func(context.Context, *core.Registry) error { return nil }); err != nil {
		t.Fatalf("drive with a complete configuration: %v", err)
	}
	err := driveWith(t, &fakeReader{cfg: moduleConfig{ClientID: "id"}}, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no secret")
		return nil
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("drive error = %v, want authn.ErrInvalidConfig", err)
	}
}

// TestPrepareStandsDownWhenNoCredentialIsGiven holds that neither credential means the module is absent without failing startup.
func TestPrepareStandsDownWhenNoCredentialIsGiven(t *testing.T) {
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
		t.Fatalf("drive with no credentials: %v", err)
	}
}

// TestPrepareNamesTheMissingCredentialWhenHalfConfigured holds that one credential alone fails startup and names the missing item.
func TestPrepareNamesTheMissingCredentialWhenHalfConfigured(t *testing.T) {
	cases := []struct {
		name   string
		reader *fakeReader
		item   string
	}{
		{"no client-id", &fakeReader{cfg: moduleConfig{ClientSecret: testClientSecret}}, clientIDItem},
		{"no client-secret", &fakeReader{cfg: moduleConfig{ClientID: testClientID}}, clientSecretItem},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := driveWith(t, tc.reader, func(context.Context, *core.Registry) error {
				t.Error("the drive reached its action with half a configuration")
				return nil
			})
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("drive error = %v, want authn.ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+tc.item) {
				t.Errorf("the failure %q does not name %s.%s", err, ConfigNamespace, tc.item)
			}
		})
	}
}

// TestPrepareRefusesAnIllegalPublicAddress holds that a public address that is not an origin fails the start.
func TestPrepareRefusesAnIllegalPublicAddress(t *testing.T) {
	reader := &fakeReader{
		cfg: moduleConfig{ClientID: testClientID, ClientSecret: testClientSecret},
		gm:  gateMount{PublicURL: "not an origin"},
	}
	err := driveWith(t, reader, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with an illegal public address")
		return nil
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("drive error = %v, want authn.ErrInvalidConfig", err)
	}
}

// TestARealRegistryDeliversTheGoogleMethod holds that the method is delivered
// and is wired to the configuration the registry resolved, not to nothing.
func TestARealRegistryDeliversTheGoogleMethod(t *testing.T) {
	var methods []authn.LoginMethod
	err := driveWith(t, validReader(), func(_ context.Context, reg *core.Registry) error {
		methods = core.ResolveAll[authn.LoginMethod](reg)
		return nil
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != "google" {
		t.Fatalf("delivered %v, want one login method named google", methods)
	}
	p, ok := methods[0].(*product)
	if !ok {
		t.Fatalf("the delivered method is %T, not this module's product", methods[0])
	}
	if p.config.clientID != testClientID || p.config.redirectURL != "https://app.example.com/api/v1/auth/callback/google" {
		t.Errorf("the product is wired to %+v, want the resolved configuration", p.config)
	}
}

// TestTheClientSecretIsRegisteredAsARedactionRule holds that the secret is masked in logs, and a build without a logger still succeeds.
func TestTheClientSecretIsRegisteredAsARedactionRule(t *testing.T) {
	recorder := newRecordingLogger()
	reg := core.New()
	reg.Register(Module())
	reg.Register(standInConfig())
	reg.Register(standInLog())
	reg.Supply(validReader(), configModule)
	reg.Supply(recorder, logModule)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, configModule, logModule},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	match, ok := recorder.patterns[ModuleName+"."+clientSecretItem]
	if !ok {
		t.Fatalf("no redaction rule was registered under %s.%s", ModuleName, clientSecretItem)
	}
	if spans := match("a failed request to https://token?client_secret=" + testClientSecret + "&x=1"); len(spans) == 0 {
		t.Error("the rule does not mask the client secret inside a URL")
	}
	if spans := match("nothing here"); len(spans) != 0 {
		t.Errorf("the rule masks a value it should not: %v", spans)
	}
}

// TestTheModuleBuildsWithNoLoggingCapability holds that an assembly with no
// logging registers nothing and still builds.
func TestTheModuleBuildsWithNoLoggingCapability(t *testing.T) {
	if err := driveWith(t, validReader(), func(context.Context, *core.Registry) error { return nil }); err != nil {
		t.Fatalf("drive with no logging capability: %v", err)
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

// TestTheProductionClientRefusesANonPublicAddress holds that the product's egress client cannot reach loopback, which reads as an outage.
func TestTheProductionClientRefusesANonPublicAddress(t *testing.T) {
	d := newIDP(t)
	var built *product
	err := driveWith(t, validReader(), func(_ context.Context, reg *core.Registry) error {
		for _, m := range core.ResolveAll[authn.LoginMethod](reg) {
			if p, ok := m.(*product); ok {
				built = p
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if built == nil {
		t.Fatal("the registry delivered no product")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		d.server.URL+"/.well-known/openid-configuration", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if resp, err := built.client.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Error("the production client reached a loopback address, want it refused")
	}
}
