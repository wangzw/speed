package mfa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// The stand-in modules an assembly drives: the capabilities another module
// delivers exclusively in a real command, each supplied to a module of its own
// here because the modules that deliver them are not ones these cases bring in.
const (
	dbModule      = "stand-in.db"
	accountModule = "stand-in.account"
	mounterModule = "stand-in.mounter"
	loggerModule  = "stand-in.log"
)

// standIn is a module that declares the tokens names, exclusively when told to,
// and delivers instance from its New. It is how a case hands the registry an
// instance for a module it does not have.
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

// factorModule registers one factor the way a factor subpackage does: a module of
// its own, delivering the factor capability non-exclusively so several stand side
// by side. The index keeps the names apart when a case registers two factors of
// one kind to reach ErrFactorConflict, because the kind is what they share.
func factorModule(i int, f authn.Factor) core.Module {
	return standIn(fmt.Sprintf("stand-in.factor.%d.%s", i, f.Kind()), f, false, (*authn.Factor)(nil))
}

// accountStandIn is one instance delivering all three capabilities the account
// module does, as its instance is.
type accountStandIn struct {
	authn.Accounts
	authn.AccountAdmin
	authn.Throttle
}

// standInLogger is a logging capability that writes every record to a buffer the
// case reads: the module takes it up in Init and warns through it in Start.
type standInLogger struct{ logs *logBuffer }

func (l standInLogger) Named(string) *slog.Logger { return l.logs.logger() }
func (l standInLogger) Redaction() log.Redaction  { return nil }

// assembly is one drive of a real registry through this module's descriptor. The
// database, the mounter, the account capabilities and the failure back-off are
// supplied as stand-in modules of their own; the factors, the settings store and
// the logging capability are what the case varies.
//
// The cases in the other files build the product by hand over a rig, which is
// what lets them reach one state at a time. What they cannot reach is the
// descriptor itself: which callback takes up which capability, what Init checks
// about the factors and the policy, and what Start checks about the register —
// and that is what a case built on this holds.
type assembly struct {
	// factors are registered as modules of their own and all take part.
	factors []authn.Factor
	// store is supplied to a stand-in for the settings module; nil leaves the
	// module with no store and every setting at its declared default.
	store settings.Store
	// logger is supplied to a stand-in for the logging module; nil leaves the
	// module writing to the process default.
	logging log.Logger
	// migrate builds the module's table before the drive, so that Start's probe
	// finds it. A case that wants the table missing leaves it false.
	migrate bool
	// seed runs on the handle once the table is built and before the drive takes
	// part, which is how a case puts rows there that only a running system would
	// have written.
	seed func(t *testing.T, handle *gorm.DB)
}

// drive advances the assembly to Start, runs action there, and returns what the
// drive returned once every Stop and Close has run. The delivery declaration is
// held inside the drive: a product that does not deliver what the descriptor
// claims makes the drive fail before action runs.
func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "mfa.db"), nil)
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	if a.migrate {
		applyMigrations(t, handle, sqliteDialect)
	}
	if a.seed != nil {
		a.seed(t, handle)
	}

	accounts := &fakeAccounts{required: map[authn.AccountID]bool{}}
	admin := &fakeAdmin{accounts: accounts}
	throttle := newThrottle()
	mounter := mounttest.New(t)
	store := a.store

	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}

	reg.Register(standIn(dbModule, database, true, (*db.Database)(nil)))
	names = append(names, dbModule)

	reg.Register(standIn(accountModule, accountStandIn{accounts, admin, throttle}, true,
		(*authn.Accounts)(nil), (*authn.AccountAdmin)(nil), (*authn.Throttle)(nil)))
	names = append(names, accountModule)

	reg.Register(standIn(mounterModule, mounter, true, (*mount.Mounter)(nil)))
	names = append(names, mounterModule)

	if store != nil {
		reg.Register(standIn("stand-in.settings", store, true, (*settings.Store)(nil)))
		names = append(names, "stand-in.settings")
	}
	if a.logging != nil {
		reg.Register(standIn(loggerModule, a.logging, false, (*log.Logger)(nil)))
		names = append(names, loggerModule)
	}
	for i, f := range a.factors {
		m := factorModule(i, f)
		reg.Register(m)
		names = append(names, m.Name)
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// nothing is an action that only lets the drive arrive.
func nothing(context.Context, *core.Registry) error { return nil }

// TestTheDescriptorNamesWhatItDeliversAndRequires holds the descriptor's shape:
// the multi-factor capability is claimed exclusively, the account lifecycle hook
// travels with it without being claimed, and the required capabilities are the
// ones another module delivers exclusively, so the closure does not pull them in
// and the host writes them down.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.mfa" {
		t.Errorf("module name = %q (constant %q), want authn.mfa", m.Name, ModuleName)
	}
	if len(m.Provides) != 2 {
		t.Fatalf("Provides = %+v, want the multi-factor capability and the lifecycle hook", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.MFA)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.MFA exclusively: one module composes the level", p)
	}
	if p := m.Provides[1]; p.Token != core.Token((*authn.AccountLifecycle)(nil)) || p.Exclusive {
		t.Errorf("Provides[1] = %+v, want authn.AccountLifecycle and not exclusive: it stands beside "+
			"the other hooks", p)
	}

	wantRequired := []core.Token{
		(*db.Database)(nil), (*mount.Mounter)(nil), (*authn.Accounts)(nil),
		(*authn.AccountAdmin)(nil), (*authn.Throttle)(nil),
	}
	var required []core.Token
	for _, r := range m.Requires {
		if !r.Optional {
			required = append(required, r.Token)
		}
	}
	if len(required) != len(wantRequired) {
		t.Fatalf("Requires = %+v, want the required capabilities %v", m.Requires, wantRequired)
	}
	for i, tk := range wantRequired {
		if required[i] != tk {
			t.Errorf("Requires[%d] = %v, want %v", i, required[i], tk)
		}
	}
	if len(m.Requires) != len(wantRequired)+2 || !m.Requires[len(m.Requires)-2].Optional || !m.Requires[len(m.Requires)-1].Optional {
		t.Errorf("Requires = %+v, want the settings store and the logging capability as the optional dependencies", m.Requires)
	}
}

// TestPrepareStatesEnabledAndNothingElse holds the stance: the module claims the
// capability exclusively, and an exclusive provider that states StateAuto is the
// cell resolution disables.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	got, err := Module().Prepare(t.Context(), core.New())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess holds the init: a host
// that imports the package finds the module in the process registry.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 2 || got.Provides[0].Token != core.Token((*authn.MFA)(nil)) {
		t.Errorf("the registered module delivers %+v, want the multi-factor capability", got.Provides)
	}
}

// TestARealRegistryDeliversMultiFactorAndTheAccountLifecycleHook holds the
// descriptor end to end: a real registry built the module, its Init took up the
// factor, the settings and the session capabilities, and the one instance it
// built answers for both the multi-factor capability and the lifecycle hook —
// which is what core's own delivery check asserted as the drive ran.
func TestARealRegistryDeliversMultiFactorAndTheAccountLifecycleHook(t *testing.T) {
	var deliveredMFA authn.MFA
	var deliveredHook authn.AccountLifecycle
	err := assembly{
		factors: []authn.Factor{newFactor(authn.FactorTOTP)},
		migrate: true,
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		if deliveredMFA, err = core.Resolve[authn.MFA](reg); err != nil {
			return err
		}
		deliveredHook, err = core.Resolve[authn.AccountLifecycle](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if deliveredMFA == nil || deliveredHook == nil {
		t.Fatal("the registry delivered nothing for the capabilities the descriptor claims")
	}
	if any(deliveredMFA) != any(deliveredHook) {
		t.Errorf("the two capabilities are delivered by %T and %T, want one instance", deliveredMFA, deliveredHook)
	}
}

// TestTwoFactorsOfOneKindFailTheDrive holds ErrFactorConflict: two implementations
// of one kind are a declaration that is not well formed, and the drive names both.
func TestTwoFactorsOfOneKindFailTheDrive(t *testing.T) {
	err := assembly{
		factors: []authn.Factor{newFactor(authn.FactorTOTP), newFactor(authn.FactorTOTP)},
		migrate: true,
	}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with two factors of one kind")
		return nil
	})
	if !errors.Is(err, authn.ErrFactorConflict) {
		t.Errorf("the drive returned %v, want ErrFactorConflict", err)
	}
}

// TestAFactorClaimingAKindOutsideTheSetFailsTheDrive holds the other malformed
// declaration: a kind that is not one of the closed set cannot be a row's kind
// and cannot be an implementation.
func TestAFactorClaimingAKindOutsideTheSetFailsTheDrive(t *testing.T) {
	err := assembly{
		factors: []authn.Factor{newFactor(authn.FactorKind("sms-2020"))},
		migrate: true,
	}.drive(t, nothing)
	if !errors.Is(err, authn.ErrInvalidClaim) {
		t.Errorf("the drive returned %v, want ErrInvalidClaim", err)
	}
}

// TestNoFactorWithTheMandatorySwitchOnFailsTheDrive holds ErrNoFactor from Init:
// a command that requires a factor of every account and takes in no factor module
// cannot satisfy its own policy, and the failure is named before anything runs.
func TestNoFactorWithTheMandatorySwitchOnFailsTheDrive(t *testing.T) {
	store := newStore()
	store.set(globalRequiredPath, true)
	err := assembly{store: store, migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with no factor and the switch on")
		return nil
	})
	if !errors.Is(err, authn.ErrNoFactor) {
		t.Errorf("the drive returned %v, want ErrNoFactor", err)
	}
}

// TestNoFactorWithTheSwitchOffIsALegalAssembly holds the absence that is not a
// failure: no factor module and no requirement is a deployment where no second
// factor is taken in, and the drive arrives.
func TestNoFactorWithTheSwitchOffIsALegalAssembly(t *testing.T) {
	reached := false
	err := assembly{migrate: true}.drive(t, func(context.Context, *core.Registry) error {
		reached = true
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if !reached {
		t.Error("the drive did not reach its action with no factor and the switch off")
	}
}

// TestStartRefusesWhenTheRegisterTableIsMissing holds ErrNotMigrated: a table
// module whose table cannot be read ends the startup and names the migration
// command rather than serving requests against a register that is not there.
func TestStartRefusesWhenTheRegisterTableIsMissing(t *testing.T) {
	err := assembly{factors: []authn.Factor{newFactor(authn.FactorTOTP)}}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with the table missing")
		return nil
	})
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestStartRefusesWhenAccountsHoldAKindNoModuleDelivers holds the other ErrNoFactor:
// the register holds an active factor whose kind no module taken in delivers, so
// no login that needs it could be completed; the failure names the kinds.
func TestStartRefusesWhenAccountsHoldAKindNoModuleDelivers(t *testing.T) {
	err := assembly{
		migrate: true,
		seed: func(t *testing.T, handle *gorm.DB) {
			t.Helper()
			if err := handle.Exec("INSERT INTO authn_factors (id, account_id, kind, label, status, created_at) "+
				"VALUES (?, ?, ?, '', ?, ?)", "f1", string(acct), string(authn.FactorTOTP), int(authn.FactorActive), base).Error; err != nil {
				t.Fatalf("seeding a factor row: %v", err)
			}
		},
	}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with an active kind no module delivers")
		return nil
	})
	if !errors.Is(err, authn.ErrNoFactor) {
		t.Errorf("the drive returned %v, want ErrNoFactor", err)
	}
}

// TestStartWarnsWhenTheAdmissionLayerTakesNoPart holds the startup diagnostic: a
// deployment with no tenant admission layer has nobody executing a tenant's
// requirement of multi-factor, and the module says so with a Warn naming that
// module.
func TestStartWarnsWhenTheAdmissionLayerTakesNoPart(t *testing.T) {
	logs := &logBuffer{}
	err := assembly{
		factors: []authn.Factor{newFactor(authn.FactorTOTP)},
		migrate: true,
		logging: standInLogger{logs: logs},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got := logs.lines("WARN", "tenant.admission", "multi-factor"); len(got) == 0 {
		t.Errorf("Start wrote no Warn that tenant.admission is not taking part; the log was:\n%s", logs.String())
	}
}

// TestStartTakesUndeliveredKindsAsUnavailableWithoutFailing holds the diagnostic
// for a kind accounts hold but this command does not: it is a factor that is
// unavailable, said with a Warn, and not a startup failure, because a factor
// module taken out is a legal assembly the operators are told about.
func TestStartTakesUndeliveredKindsAsUnavailableWithoutFailing(t *testing.T) {
	logs := &logBuffer{}
	err := assembly{
		factors: []authn.Factor{newFactor(authn.FactorSMS)},
		migrate: true,
		logging: standInLogger{logs: logs},
		seed: func(t *testing.T, handle *gorm.DB) {
			t.Helper()
			if err := handle.Exec("INSERT INTO authn_factors (id, account_id, kind, label, status, created_at) "+
				"VALUES (?, ?, ?, '', ?, ?)", "f1", string(acct), string(authn.FactorTOTP), int(authn.FactorActive), base).Error; err != nil {
				t.Fatalf("seeding a factor row: %v", err)
			}
		},
	}.drive(t, nothing)
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if got := logs.lines("WARN", "totp"); len(got) == 0 {
		t.Errorf("Start wrote no Warn about the TOTP factors no module delivers; the log was:\n%s", strings.TrimSpace(logs.String()))
	}
}

// Start says which factor kinds take part: a deployment with none taken in has
// the empty-assembly line, and one with a factor taken in names the kinds that
// do. Which line a deployment gets is what tells an operator whether any factor
// is in play.
func TestStartSaysWhichFactorKindsTakePart(t *testing.T) {
	empty := &logBuffer{}
	if err := (assembly{migrate: true, logging: standInLogger{logs: empty}}).drive(t, nothing); err != nil {
		t.Fatalf("the drive with no factor failed: %v", err)
	}
	if got := empty.lines("no factor kind takes part"); len(got) == 0 {
		t.Errorf("Start did not say no factor kind takes part; the log was:\n%s", strings.TrimSpace(empty.String()))
	}

	with := &logBuffer{}
	if err := (assembly{factors: []authn.Factor{newFactor(authn.FactorTOTP)}, migrate: true, logging: standInLogger{logs: with}}).drive(t, nothing); err != nil {
		t.Fatalf("the drive with a factor failed: %v", err)
	}
	if got := with.lines("the factor kinds taking part", "totp"); len(got) == 0 {
		t.Errorf("Start did not name the factor kind taking part; the log was:\n%s", strings.TrimSpace(with.String()))
	}
}
