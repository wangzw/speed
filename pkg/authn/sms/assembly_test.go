package sms

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
)

const (
	dbModule       = "stand-in.db"
	registryModule = "stand-in.registry"
	senderModule   = "stand-in.sender"
	humanModule    = "stand-in.human"
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

type assembly struct {
	sender        sms.Sender
	registry      sms.Registry
	secondSender  bool
	omitHuman     bool
	store         settings.Store
	keyless       bool
	migrate       bool
	body          string
	readerModules []core.Module
}

func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	var items dbtest.Items
	if !a.keyless {
		items = dbtest.Items{"encryption-key": rootKey}
	}
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), items)
	if a.migrate {
		handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
		applyMigrations(t, handle, sqliteDialect)
	}

	body := a.body
	if body == "" {
		body = `{"sms":{"carrier":"log"}}`
	}
	modules := append([]core.Module{Module(), selectionModule()}, a.readerModules...)
	reader := authntest.ConfigReaderAll(t, body, modules...)

	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}

	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names = append(names, config.ModuleName)

	reg.Register(standIn(dbModule, database, true, (*db.Database)(nil)))
	names = append(names, dbModule)

	reg.Register(budget.Module())
	names = append(names, budget.ModuleName)

	if a.registry != nil {
		reg.Register(standIn(registryModule, a.registry, true, (*sms.Registry)(nil)))
		names = append(names, registryModule)
	}
	if a.sender != nil {
		reg.Register(standIn(senderModule, a.sender, true, (*sms.Sender)(nil)))
		names = append(names, senderModule)
	}
	if a.secondSender {
		reg.Register(standIn("stand-in.sender2", newSender(), true, (*sms.Sender)(nil)))
		names = append(names, "stand-in.sender2")
	}
	if a.store != nil {
		reg.Register(standIn("stand-in.settings", a.store, true, (*settings.Store)(nil)))
		names = append(names, "stand-in.settings")
	}
	if !a.omitHuman {
		reg.Register(standIn(humanModule, &fakeHuman{required: false, provider: "off"}, true, (*authn.HumanVerifier)(nil)))
		names = append(names, humanModule)
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// TestTheDescriptorNamesWhatItDeliversAndRequires checks that the descriptor claims SMSCodes exclusively and requires the ledger, registry, sender and gate.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.sms" {
		t.Errorf("module name = %q (constant %q), want authn.sms", m.Name, ModuleName)
	}
	if len(m.Provides) != 2 {
		t.Fatalf("Provides = %+v, want the SMS capability and the lifecycle hook", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.SMSCodes)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.SMSCodes exclusively", p)
	}
	if p := m.Provides[1]; p.Token != core.Token((*authn.AccountLifecycle)(nil)) || p.Exclusive {
		t.Errorf("Provides[1] = %+v, want authn.AccountLifecycle and not exclusive", p)
	}

	var required []core.Token
	for _, r := range m.Requires {
		if !r.Optional {
			required = append(required, r.Token)
		}
	}
	want := []core.Token{
		(*db.Database)(nil), (*sms.Registry)(nil), (*sms.Sender)(nil), (*budget.Store)(nil),
		(*authn.HumanVerifier)(nil),
	}
	if len(required) != len(want) {
		t.Fatalf("Requires = %+v, want %v", m.Requires, want)
	}
	for i, tk := range want {
		if required[i] != tk {
			t.Errorf("Requires[%d] = %v, want %v", i, required[i], tk)
		}
	}
	optional := m.Requires[len(want):]
	if len(optional) != 2 {
		t.Fatalf("Requires = %+v, want the settings store and the logging capability as the optional dependencies", m.Requires)
	}
	for _, r := range optional {
		if !r.Optional {
			t.Errorf("the requirement %+v is not optional", r)
		}
	}
}

func selectionModule() core.Module {
	return core.Module{Name: "test.selection", Resources: []any{sms.SelectionSchema()}}
}

// TestPrepareStatesEnabledAndNothingElse checks that Prepare states the enabled stance and nothing else.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	reader := authntest.ConfigReaderAll(t, `{"sms":{"carrier":"log"}}`, Module(), selectionModule())
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

// TestPrepareStandsDownWhenNeitherCarrierNorTemplateIsConfigured checks that Prepare stands down when neither a carrier nor a template is configured.
func TestPrepareStandsDownWhenNeitherCarrierNorTemplateIsConfigured(t *testing.T) {
	reader := authntest.ConfigReaderAll(t, "{}", Module())
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

// TestPrepareRefusesAConfigurationItCannotRunOn checks that Prepare refuses a configuration the module cannot run on.
func TestPrepareRefusesAConfigurationItCannotRunOn(t *testing.T) {
	reader := authntest.ConfigReaderAll(t, `{"authn":{"sms":{"workers":0}}}`, Module())
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	_, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Errorf("Prepare on zero workers returned %v, want ErrInvalidConfig", err)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess holds the init.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 2 || got.Provides[0].Token != core.Token((*authn.SMSCodes)(nil)) {
		t.Errorf("the registered module delivers %+v, want the SMS capability", got.Provides)
	}
}

// TestARealRegistryDeliversSMSCodesAndTheHook checks that a real registry delivers SMSCodes and the account lifecycle hook.
func TestARealRegistryDeliversSMSCodesAndTheHook(t *testing.T) {
	var codes authn.SMSCodes
	var hook authn.AccountLifecycle
	err := assembly{
		sender:   newSender(),
		registry: newRegistry(),
		store:    newStore(),
		migrate:  true,
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		if codes, err = core.Resolve[authn.SMSCodes](reg); err != nil {
			return err
		}
		hook, err = core.Resolve[authn.AccountLifecycle](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if codes == nil || hook == nil {
		t.Fatal("the registry delivered nothing for the capabilities the descriptor claims")
	}
	if any(codes) != any(hook) {
		t.Errorf("the two capabilities are delivered by %T and %T, want one instance", codes, hook)
	}
}

// TestTheDriveFailsWithoutASender checks that the drive fails without a sender.
func TestTheDriveFailsWithoutASender(t *testing.T) {
	err := assembly{registry: newRegistry(), migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no sending seam")
		return nil
	})
	if err == nil {
		t.Fatal("the drive succeeded with no sending seam, want a resolution failure")
	}
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("the drive returned %v, want a missing-provider failure", err)
	}
}

// TestTheDriveFailsWithoutARegistry checks that the drive fails without a registry.
func TestTheDriveFailsWithoutARegistry(t *testing.T) {
	err := assembly{sender: newSender(), migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no template registry")
		return nil
	})
	if err == nil {
		t.Fatal("the drive succeeded with no template registry, want a resolution failure")
	}
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("the drive returned %v, want a missing-provider failure", err)
	}
}

// TestTheDriveFailsWithoutAHumanVerifier checks that the drive fails without a human-verification gate.
func TestTheDriveFailsWithoutAHumanVerifier(t *testing.T) {
	err := assembly{
		sender:    newSender(),
		registry:  newRegistry(),
		migrate:   true,
		omitHuman: true,
	}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no human-verification gate")
		return nil
	})
	if err == nil {
		t.Fatal("the drive succeeded with no human-verification gate, want a resolution failure")
	}
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("the drive returned %v, want a missing-provider failure", err)
	}
	if !strings.Contains(err.Error(), "HumanVerifier") {
		t.Errorf("the failure %q does not name the missing human-verification gate", err.Error())
	}
}

// TestTwoSendersFailInResolution checks that two senders fail in resolution.
func TestTwoSendersFailInResolution(t *testing.T) {
	err := assembly{
		sender:       newSender(),
		secondSender: true,
		registry:     newRegistry(),
		migrate:      true,
	}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with two carriers")
		return nil
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Errorf("the drive returned %v, want ErrExclusiveViolated", err)
	}
}

// TestStartRefusesWhenTheTableIsMissingThroughTheDrive checks that Start refuses when the table is missing.
func TestStartRefusesWhenTheTableIsMissingThroughTheDrive(t *testing.T) {
	err := assembly{sender: newSender(), registry: newRegistry()}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with the table missing")
		return nil
	})
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestStartRefusesWithoutARootKeyThroughTheDrive checks that Start refuses without a root key.
func TestStartRefusesWithoutARootKeyThroughTheDrive(t *testing.T) {
	err := assembly{sender: newSender(), registry: newRegistry(), keyless: true, migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no root key")
		return nil
	})
	if !errors.Is(err, authn.ErrNoEncryptionKey) {
		t.Errorf("the drive returned %v, want ErrNoEncryptionKey", err)
	}
}

// TestTheDriveRefusesARegistryWhoseDeclarationDoesNotMatchWhatTheModuleNeeds checks that the drive refuses a registry whose declaration does not match what the module sends.
func TestTheDriveRefusesARegistryWhoseDeclarationDoesNotMatchWhatTheModuleNeeds(t *testing.T) {
	cases := []struct {
		name     string
		drop     func(declared []sms.Template) []sms.Template
		mentions string
	}{
		{
			name:     "a missing template",
			drop:     func(d []sms.Template) []sms.Template { return d[:len(d)-1] },
			mentions: templateEnroll,
		},
		{
			name: "a template missing the default language",
			drop: func(d []sms.Template) []sms.Template {
				d[0].Locales = d[0].Locales[:1]
				return d
			},
			mentions: testDefaultLocale,
		},
		{
			name: "a template missing a parameter",
			drop: func(d []sms.Template) []sms.Template {
				d[0].Params = d[0].Params[:1]
				return d
			},
			mentions: "ttl_minutes",
		},
		{
			name: "a template with a parameter the module never sends",
			drop: func(d []sms.Template) []sms.Template {
				d[0].Params = append(append([]string(nil), d[0].Params...), "sign")
				return d
			},
			mentions: "sign",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			registry := newRegistry()
			registry.setDeclared(c.drop(registry.Declared()))
			err := assembly{registry: registry, sender: newSender(), migrate: true}.drive(t, func(context.Context, *core.Registry) error {
				t.Error("the drive reached its action with a registry that does not cover the module's templates")
				return nil
			})
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("the drive returned %v, want ErrInvalidConfig", err)
			}
			if !strings.Contains(err.Error(), c.mentions) {
				t.Errorf("the failure %q does not name the missing combination %q", err.Error(), c.mentions)
			}
		})
	}
}

type i18nStubDefaults struct {
	DefaultLocale string `config:"default-locale"`
}

func i18nStubModule() core.Module {
	return core.Module{
		Name: "stand-in.i18n",
		Resources: []any{config.Schema{
			Namespace: i18n.ConfigNamespace,
			Mounts:    []config.Mount{{Value: &i18nStubDefaults{DefaultLocale: defaultLocaleFallback}}},
			Items: map[string]config.Item{
				i18n.DefaultLocaleItem: {Description: "the default locale"},
			},
		}},
	}
}

// TestStartupRefusesARegistryMissingTheConfiguredDefaultLanguage checks that the startup refuses a registry missing the configured default language.
func TestStartupRefusesARegistryMissingTheConfiguredDefaultLanguage(t *testing.T) {
	registry := newRegistry()
	err := assembly{
		sender:        newSender(),
		registry:      registry,
		store:         newStore(),
		migrate:       true,
		body:          `{"sms":{"carrier":"log"},"i18n":{"default-locale":"ja-JP"}}`,
		readerModules: []core.Module{i18nStubModule()},
	}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with a registry that misses the configured default language")
		return nil
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the drive returned %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "ja-JP") {
		t.Errorf("the failure %q does not name the missing default language ja-JP", err.Error())
	}
}

// TestASendGoesOutInALanguageTheRegistryDeclaresBesidesChineseAndEnglish checks that a send goes out in another language the registry declares.
func TestASendGoesOutInALanguageTheRegistryDeclaresBesidesChineseAndEnglish(t *testing.T) {
	registry := newRegistry()
	registry.setDeclared(declaredTemplatesFor([]string{"en-US", "ja-JP"}))
	if err := checkSender(registry.Declared(), testDefaultLocale); err != nil {
		t.Fatalf("a registry declaring en-US and ja-JP was refused: %v", err)
	}
	r := newRigOver(t, openSQLite(t), registry, newSender(), testDefaultLocale)
	r.allow()
	if _, err := r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin, Phone: phone, Locale: "ja-JP", Deliver: true,
	}); err != nil {
		t.Fatalf("a ja-JP send returned %v, want success", err)
	}
	msg := <-r.p.pool.queue
	if msg.Locale != "ja-JP" {
		t.Errorf("the queued message locale = %q, want ja-JP", msg.Locale)
	}
}

// TestASendWithNoNamedLocaleGoesOutInTheConfiguredDefault checks that a send with no named locale uses the configured default.
func TestASendWithNoNamedLocaleGoesOutInTheConfiguredDefault(t *testing.T) {
	sender := newSender()
	registry := newRegistry()
	registry.setDeclared(declaredTemplatesFor([]string{"en-US", "ja-JP"}))
	store := newStore()
	store.set(allowedPrefixesPath, `["+86"]`)
	err := assembly{
		sender:        sender,
		registry:      registry,
		store:         store,
		migrate:       true,
		body:          `{"sms":{"carrier":"log"},"i18n":{"default-locale":"ja-JP"}}`,
		readerModules: []core.Module{i18nStubModule()},
	}.drive(t, func(ctx context.Context, reg *core.Registry) error {
		codes, err := core.Resolve[authn.SMSCodes](reg)
		if err != nil {
			return err
		}
		_, err = codes.Send(ctx, authn.SendCode{Purpose: authn.SMSPurposeLogin, Phone: phone, Deliver: true})
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	msg, ok := sender.last()
	if !ok {
		t.Fatal("the carrier was handed no message")
	}
	if msg.Locale != "ja-JP" {
		t.Errorf("the sent message locale = %q, want the configured default ja-JP", msg.Locale)
	}
}
