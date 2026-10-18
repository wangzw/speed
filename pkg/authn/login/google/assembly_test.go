package google

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// The stand-ins for the modules this descriptor names: the config module, whose
// reader carries the credentials, and the logging capability, with which the
// client secret is registered as a redaction rule.
const (
	configModule = "stand-in.config"
	logModule    = "stand-in.log"
)

// standInLog declares the logging capability the way the log module does.
func standInLog() core.Module {
	return core.Module{
		Name:     logModule,
		Provides: []core.Provision{{Token: (*log.Logger)(nil)}},
	}
}

// recordingLogger is a logging capability that records the redaction rules
// registered with it.
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

// standInConfig declares the configuration reader the way the config module
// does.
func standInConfig() core.Module {
	return core.Module{
		Name:     configModule,
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	}
}

// fakeReader is a configuration reader whose sections a case chooses. It
// decodes the two paths this module reads and leaves anything else alone.
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
		if c, ok := target.(*moduleConfig); ok {
			*c = f.cfg
		}
	case gateNamespace + "." + gateMountPath:
		if gm, ok := target.(*gateMount); ok {
			*gm = f.gm
		}
	}
	return nil
}

// validReader is a reader with a complete configuration.
func validReader() *fakeReader {
	return &fakeReader{
		cfg: moduleConfig{ClientID: testClientID, ClientSecret: testClientSecret},
		gm:  gateMount{PublicURL: "https://app.example.com"},
	}
}

// driveWith registers this module beside the stand-in config module and drives a
// real registry to the stage where the instance exists, handing the registry to
// action.
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

// TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt holds the descriptor
// shape: the login method capability alone, delivered non-exclusively, and no
// capability required of another module.
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

// TestPrepareStatesEnabledAndRefusesAnIncompleteConfiguration holds that a
// complete configuration enables the module and an incomplete one fails the
// start.
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

// TestTheClientSecretIsRegisteredAsARedactionRule holds that the client secret
// is registered with the logging capability as a literal rule, so it is masked
// wherever it turns up in a log line, and that a build with no logging
// capability still succeeds.
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

// TestTheProductionClientRefusesANonPublicAddress holds that the client newProduct
// builds is the restricted egress client: driven through the real registry, the
// product's own client refuses the loopback mock, which is the provider being
// unreachable and not a refusal.
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
