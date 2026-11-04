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

// The stand-in modules an assembly drives: the capabilities another module
// delivers exclusively in a real command, each supplied to a module of its own
// here because the modules that deliver them are not ones these cases bring in.
const (
	dbModule       = "stand-in.db"
	registryModule = "stand-in.registry"
	senderModule   = "stand-in.sender"
	humanModule    = "stand-in.human"
)

// standIn is a module that declares the tokens names, exclusively when told to,
// and delivers instance from its New.
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

// assembly is one drive of a real registry through this module's descriptor. The
// database, the sending seam and the human-verification gate are supplied as
// stand-in modules of their own; what the case varies is whether each is present
// and whether the table and the root key are there.
type assembly struct {
	// sender, when non-nil, is supplied to a stand-in for the carrier module.
	sender sms.Sender
	// registry, when non-nil, is supplied to a stand-in for the template registry
	// module.
	registry sms.Registry
	// secondSender adds a second carrier stand-in, for the exclusive-conflict
	// case.
	secondSender bool
	// omitHuman leaves the human-verification gate out, for the case that holds a
	// command naming this module without one to a resolution refusal.
	omitHuman bool
	// store is supplied to a stand-in for the settings module.
	store settings.Store
	// keyless builds the database without a root key.
	keyless bool
	// migrate builds the module's table before the drive.
	migrate bool
	// body is the primary configuration source the reader is loaded from; empty
	// is the empty document, whose defaults are what the module runs on.
	body string
	// readerModules are modules whose configuration schemas the reader is also
	// built with, so a case can declare a namespace this module reads but does not
	// own — the i18n capability's default-locale item, for one.
	readerModules []core.Module
}

// drive advances the assembly to Start, runs action there, and returns what the
// drive returned once every Stop and Close has run.
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
		// A named carrier is a deployment that asks for SMS; without one an empty
		// document is the all-empty configuration the module stands down on.
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

	// The shared ledger is a required, non-exclusive provider: one process has
	// one ledger and both sending channels read it. The real descriptor is
	// registered, so the case proves the requirement resolves against the
	// module a deployment uses rather than a stand-in.
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
		// The gate is a required dependency. An off gate is the composition of the
		// humanverify module with both surfaces at mode off, which is the
		// assembly these cases run: none of them asks about a proof.
		reg.Register(standIn(humanModule, &fakeHuman{required: false, provider: "off"}, true, (*authn.HumanVerifier)(nil)))
		names = append(names, humanModule)
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// TestTheDescriptorNamesWhatItDeliversAndRequires holds the descriptor's shape:
// the SMS capability is claimed exclusively, the account lifecycle hook travels
// with it without being claimed, and the database, the template registry, the
// sending seam, the shared ledger and the human-verification gate are required
// — each delivered exclusively by another module except the ledger, so the
// closure does not pull them in.
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

// selectionModule declares the carrier selection item, which in a real binary
// the log carrier declares. A case that configures a carrier needs it in the
// manifest or the written value would have no declared path.
func selectionModule() core.Module {
	return core.Module{Name: "test.selection", Resources: []any{sms.SelectionSchema()}}
}

// TestPrepareStatesEnabledAndNothingElse holds the stance, given the reader that
// sizes the pool and a selection that asks for a carrier.
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

// TestPrepareStandsDownWhenNeitherCarrierNorTemplateIsConfigured holds the
// all-empty case: no carrier selection and no template is a deployment that did
// not configure SMS, so the module stands itself down with the reason instead of
// failing construction on a template it cannot find.
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

// TestPrepareRefusesAConfigurationItCannotRunOn holds that Prepare validates the
// pool sizing: a command whose configuration names no worker fails before
// anything runs.
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

// TestARealRegistryDeliversSMSCodesAndTheHook holds the descriptor end to end: a
// real registry built the module, its Init took up the optional capabilities, and
// the one instance answers for both the SMS capability and the lifecycle hook.
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

// TestTheDriveFailsWithoutASender holds the required exclusive dependency: a
// command that names this module and no carrier fails in resolution.
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

// TestTheDriveFailsWithoutARegistry holds the second required exclusive
// dependency: a command that names this module and no template registry fails in
// resolution, because the module has no declaration to check its templates
// against and no registry to resolve a message through.
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

// TestTheDriveFailsWithoutAHumanVerifier holds the required gate: a command that
// names this module and no human-verification gate fails in resolution. Before
// the requirement the drive succeeded, so a deployment whose configuration named
// a mode ran its sends ungated.
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

// TestTwoSendersFailInResolution holds the exclusivity: two carriers at once are a
// declaration the resolution refuses, so a host picks one.
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

// TestStartRefusesWhenTheTableIsMissingThroughTheDrive holds ErrNotMigrated from
// Start, naming the migration command.
func TestStartRefusesWhenTheTableIsMissingThroughTheDrive(t *testing.T) {
	err := assembly{sender: newSender(), registry: newRegistry()}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with the table missing")
		return nil
	})
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestStartRefusesWithoutARootKeyThroughTheDrive holds ErrNoEncryptionKey from
// Start.
func TestStartRefusesWithoutARootKeyThroughTheDrive(t *testing.T) {
	err := assembly{sender: newSender(), registry: newRegistry(), keyless: true, migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no root key")
		return nil
	})
	if !errors.Is(err, authn.ErrNoEncryptionKey) {
		t.Errorf("the drive returned %v, want ErrNoEncryptionKey", err)
	}
}

// TestTheDriveRefusesARegistryWhoseDeclarationDoesNotMatchWhatTheModuleNeeds
// holds the consumer-side check: this module sends under three templates, each
// with two languages and exactly the two parameters it builds, and a registry
// whose declaration does not match — a missing template, a missing language, a
// missing parameter, or a parameter the module never sends — stops the startup
// at New with ErrInvalidConfig naming the combination it is about, ahead of any
// code. A mismatch is named at startup rather than at the first send that needs
// it.
func TestTheDriveRefusesARegistryWhoseDeclarationDoesNotMatchWhatTheModuleNeeds(t *testing.T) {
	cases := []struct {
		name string
		drop func(declared []sms.Template) []sms.Template
		// mentions is the combination the failure has to name: the missing
		// template, the missing language, the missing parameter or the surplus
		// parameter.
		mentions string
	}{
		{
			name:     "a missing template",
			drop:     func(d []sms.Template) []sms.Template { return d[:len(d)-1] },
			mentions: templateEnroll,
		},
		{
			// Truncating a template's languages to the first leaves one that misses
			// the configured default (the set is ordered default-last), which is what
			// checkSender refuses.
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
			// A template substituting a parameter this module never sends cannot be
			// satisfied: Resolve requires every declared parameter and the module
			// carries only its own two. A subset check passes such a template and
			// then rejects every send at runtime, where the failure is a metric and
			// a warn line and never reaches the operator.
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

// i18nStubDefaults is the carrier struct the stub schema below mounts, with the
// one item's declared default.
type i18nStubDefaults struct {
	DefaultLocale string `config:"default-locale"`
}

// i18nStubModule declares the i18n capability's configuration namespace with
// just the shared default-locale item, so a case drives the module's read of the
// item the i18n capability also reads. It is a stand-in the test file builds
// rather than the catalog module itself, which a subpackage of pkg/authn may not
// import; the namespace name and the item name are the root package's own, so
// the read goes through the same names the real catalog declares either way.
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

// TestStartupRefusesARegistryMissingTheConfiguredDefaultLanguage holds the new
// startup check: the default language is configuration, not a constant, so a
// registry whose templates do not cover it cannot send a message the module
// falls back to, and the drive stops with ErrInvalidConfig naming the missing
// pair.
//
// On the pre-change code this drive succeeds: it read no configured default and
// required only the two hardcoded languages, which this registry declares, so
// the action ran rather than failing at startup.
func TestStartupRefusesARegistryMissingTheConfiguredDefaultLanguage(t *testing.T) {
	registry := newRegistry() // declares zh-CN and en-US only
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

// TestASendGoesOutInALanguageTheRegistryDeclaresBesidesChineseAndEnglish holds
// that the set of languages is the registry's, not a hardcoded pair: a registry
// declaring en-US and ja-JP satisfies the startup check, and a ja-JP request
// goes out in ja-JP.
//
// On the pre-change code a "ja-JP" send is refused as a caller defect before it
// reaches the carrier, and the declared set would not satisfy the hardcoded
// check in the first place.
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

// TestASendWithNoNamedLocaleGoesOutInTheConfiguredDefault holds the end-to-end
// chain: the shared i18n.default-locale item is read as the run's default, and a
// send whose caller names no language goes out in it.
//
// On the pre-change code the default is the en-US constant, so this send goes
// out in en-US and the assertion fails; the pre-change check also refuses a
// declaration of ja-JP.
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
