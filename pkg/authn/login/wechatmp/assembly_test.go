package wechatmp

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

const (
	configModule = "stand-in.config"
	logModule    = "stand-in.log"
)

func standInConfig() core.Module {
	return core.Module{
		Name:     configModule,
		Provides: []core.Provision{{Token: (*config.Reader)(nil)}},
	}
}

func standInLog() core.Module {
	return core.Module{
		Name:     logModule,
		Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
	}
}

type fakeReader struct {
	appID     string
	appSecret string
	publicURL string
	basePath  string
	err       error
}

func (f *fakeReader) Decode(path string, target any) error {
	if f.err != nil {
		return f.err
	}
	switch path {
	case ConfigNamespace:
		switch t := target.(type) {
		case *moduleConfig:
			t.AppID, t.AppSecret = f.appID, f.appSecret
		case *appIDOnly:
			if f.appID == "" {
				return config.ErrMissingRequired
			}
			t.AppID = f.appID
		case *appSecretOnly:
			if f.appSecret == "" {
				return config.ErrMissingRequired
			}
			t.AppSecret = f.appSecret
		}
	case gateNamespace + ".mount":
		if g, ok := target.(*gateMount); ok {
			g.PublicURL, g.Bases.Default = f.publicURL, f.basePath
		}
	}
	return nil
}

type recordingRedaction struct {
	pattern string
	match   log.Matcher
}

func (r *recordingRedaction) AddKeys(...string) {}
func (r *recordingRedaction) AddPattern(name string, m log.Matcher) {
	r.pattern, r.match = name, m
}

type recordingLogger struct{ r *recordingRedaction }

func (l *recordingLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (l *recordingLogger) Redaction() log.Redaction  { return l.r }

var (
	_ config.Reader = (*fakeReader)(nil)
	_ log.Logger    = (*recordingLogger)(nil)
)

func mask(m log.Matcher, value string) string {
	spans := m(value)
	if len(spans) == 0 {
		return value
	}
	var b strings.Builder
	at, masked := 0, false
	for _, s := range spans {
		if s.Start < at || s.End <= s.Start {
			continue
		}
		if masked && s.Start <= at {
			at = s.End
			continue
		}
		b.WriteString(value[at:s.Start])
		b.WriteString(log.MaskedText)
		at, masked = s.End, true
	}
	b.WriteString(value[at:])
	return b.String()
}

func buildRegistry(reader config.Reader, logger log.Logger) *core.Registry {
	reg := core.New()
	reg.Register(Module())
	reg.Register(standInConfig())
	reg.Register(standInLog())
	reg.Supply(reader, configModule)
	reg.Supply(logger, logModule)
	return reg
}

func fullReader() *fakeReader {
	return &fakeReader{appID: testAppID, appSecret: testAppSecret, publicURL: "https://app.example.com"}
}

// TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt checks the non-exclusive method, optional logger and config schema.
func TestTheDescriptorDeliversTheLoginMethodWithoutClaimingIt(t *testing.T) {
	m := Module()
	if m.Name != "authn.login.wechat-mp" || ModuleName != "authn.login.wechat-mp" {
		t.Errorf("module name = %q (constant %q), want authn.login.wechat-mp", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login method capability alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethod)(nil)) || p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethod, not exclusive", p)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the logging capability alone", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want the optional logging capability", r)
	}
	if len(m.Resources) != 1 {
		t.Errorf("Resources = %v, want the module's schema", m.Resources)
	}
}

// TestPrepareStatesEnabledWhenTheConfigurationIsComplete checks that Prepare
// reports the module enabled when the configuration is complete.
func TestPrepareStatesEnabledWhenTheConfigurationIsComplete(t *testing.T) {
	reg := buildRegistry(fullReader(), &recordingLogger{r: &recordingRedaction{}})
	got, err := Module().Prepare(context.Background(), reg)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestPrepareFailsOnAMissingRequiredItem checks that Prepare fails as an invalid
// config on a missing app id, app secret or public URL, without naming the secret.
func TestPrepareFailsOnAMissingRequiredItem(t *testing.T) {
	cases := []struct {
		name   string
		reader *fakeReader
	}{
		{"no app-id", &fakeReader{appSecret: testAppSecret, publicURL: "https://app.example.com"}},
		{"no app-secret", &fakeReader{appID: testAppID, publicURL: "https://app.example.com"}},
		{"no public-url", &fakeReader{appID: testAppID, appSecret: testAppSecret}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := buildRegistry(tc.reader, &recordingLogger{r: &recordingRedaction{}})
			_, err := Module().Prepare(context.Background(), reg)
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("Prepare error = %v, want ErrInvalidConfig", err)
			}
			if err != nil && strings.Contains(err.Error(), testAppSecret) {
				t.Errorf("the failure names the secret: %v", err)
			}
		})
	}
}

// TestARealRegistryDeliversTheMethodAndBuildsTheCallbackAddress checks that a real registry delivers it and builds the callback from public URL and app id.
func TestARealRegistryDeliversTheMethodAndBuildsTheCallbackAddress(t *testing.T) {
	reg := buildRegistry(fullReader(), &recordingLogger{r: &recordingRedaction{}})
	var methods []authn.LoginMethod
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, configModule, logModule},
		Action: func(_ context.Context, reg *core.Registry) error {
			methods = core.ResolveAll[authn.LoginMethod](reg)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != "wechat.mp" {
		t.Fatalf("delivered %v, want one login method named wechat.mp", methods)
	}
	got, _ := methods[0].Begin(context.Background(), authn.Start{Purpose: authn.PurposeLogin})
	to, err := url.Parse(step.RedirectOf(got))
	if err != nil {
		t.Fatalf("the authorize address does not parse: %v", err)
	}
	if have := to.Query().Get("redirect_uri"); have != "https://app.example.com/api/v1/auth/callback/wechat.mp" {
		t.Errorf("redirect_uri = %q, want the address built from the public URL and the base path", have)
	}
	if have := to.Query().Get("appid"); have != testAppID {
		t.Errorf("appid = %q, want the configured application identifier %q", have, testAppID)
	}
}

// TestNewRegistersTheSecretAsALiteralRedactionRule checks that New registers the
// application secret as a literal redaction rule.
func TestNewRegistersTheSecretAsALiteralRedactionRule(t *testing.T) {
	red := &recordingRedaction{}
	reg := buildRegistry(fullReader(), &recordingLogger{r: red})
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, configModule, logModule},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if red.match == nil {
		t.Fatal("New registered no redaction rule")
	}
	line := "refused https://api.weixin.qq.com/sns/oauth2/access_token?secret=" + url.QueryEscape(testAppSecret) + "&code=x"
	masked := mask(red.match, line)
	if strings.Contains(masked, testAppSecret) || strings.Contains(masked, url.QueryEscape(testAppSecret)) {
		t.Errorf("the masked line %q still carries the secret", masked)
	}
	if !strings.Contains(masked, log.MaskedText) {
		t.Errorf("the masked line %q carries no mask", masked)
	}
}

// TestACommandWithoutTheConfigurationReaderFailsToBuild checks that a command
// without the configuration reader fails to build.
func TestACommandWithoutTheConfigurationReaderFailsToBuild(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName},
		Action: func(context.Context, *core.Registry) error {
			t.Error("the drive reached its action with no configuration reader")
			return nil
		},
	})
	if err == nil {
		t.Error("the drive built the module with no configuration reader")
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess checks that importing
// the package registers the module with the process registry.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.LoginMethod)(nil)) {
		t.Errorf("the registered module delivers %+v, want the login method capability", got.Provides)
	}
}

// TestPrepareStandsDownWhenNoCredentialIsGiven holds that neither credential means the method is absent without failing startup.
func TestPrepareStandsDownWhenNoCredentialIsGiven(t *testing.T) {
	reg := buildRegistry(&fakeReader{}, &recordingLogger{r: &recordingRedaction{}})
	got, err := Module().Prepare(context.Background(), reg)
	if err != nil {
		t.Fatalf("Prepare with no credentials: %v", err)
	}
	if got.State != core.StateDisabled {
		t.Errorf("Prepare = %+v, want StateDisabled", got)
	}
	if got.Reason == "" {
		t.Error("the module stood down without a reason")
	}
}

// TestPrepareRefusesAnIllegalPublicAddress holds that a public address that is not an origin stops startup.
func TestPrepareRefusesAnIllegalPublicAddress(t *testing.T) {
	reader := &fakeReader{appID: testAppID, appSecret: testAppSecret, publicURL: "not an origin"}
	reg := buildRegistry(reader, &recordingLogger{r: &recordingRedaction{}})
	_, err := Module().Prepare(context.Background(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("Prepare error = %v, want ErrInvalidConfig", err)
	}
}

// TestPreparePropagatesAReaderFailure holds that a section read failure fails the start rather than half-configuring the method.
func TestPreparePropagatesAReaderFailure(t *testing.T) {
	boom := errors.New("the section cannot be read")
	reg := buildRegistry(&fakeReader{err: boom}, &recordingLogger{r: &recordingRedaction{}})
	if _, err := Module().Prepare(context.Background(), reg); !errors.Is(err, boom) {
		t.Fatalf("Prepare with a reader failure gave %v, want the reader's error", err)
	}
}

// TestTheModuleBuildsWithNoLoggingCapability holds that an assembly with no
// logging registers nothing and still builds and delivers the method.
func TestTheModuleBuildsWithNoLoggingCapability(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	reg.Register(standInConfig())
	reg.Supply(fullReader(), configModule)

	var methods []authn.LoginMethod
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName, configModule},
		Action: func(_ context.Context, reg *core.Registry) error {
			methods = core.ResolveAll[authn.LoginMethod](reg)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("drive with no logging capability: %v", err)
	}
	if len(methods) != 1 || methods[0].Name() != "wechat.mp" {
		t.Fatalf("delivered %v, want one login method named wechat.mp", methods)
	}
}

// TestTheSchemaMarksTheCredentials holds the schema's contract: the application
// identifier is required and the secret is required and sensitive.
func TestTheSchemaMarksTheCredentials(t *testing.T) {
	s := schema()
	if !s.Items[appIDItem].Required {
		t.Error("app-id is not required")
	}
	secret := s.Items[appSecretItem]
	if !secret.Required || !secret.Sensitive {
		t.Errorf("app-secret = %+v, want required and sensitive", secret)
	}
}
