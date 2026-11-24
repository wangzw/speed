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

const (
	dbModule      = "stand-in.db"
	accountModule = "stand-in.account"
	mounterModule = "stand-in.mounter"
	loggerModule  = "stand-in.log"
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

func factorModule(i int, f authn.Factor) core.Module {
	return standIn(fmt.Sprintf("stand-in.factor.%d.%s", i, f.Kind()), f, false, (*authn.Factor)(nil))
}

type accountStandIn struct {
	authn.Accounts
	authn.AccountAdmin
	authn.Throttle
}

type standInLogger struct{ logs *logBuffer }

func (l standInLogger) Named(string) *slog.Logger { return l.logs.logger() }
func (l standInLogger) Redaction() log.Redaction  { return nil }

type assembly struct {
	factors []authn.Factor
	store   settings.Store
	logging log.Logger
	migrate bool
	seed    func(t *testing.T, handle *gorm.DB)
}

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

func nothing(context.Context, *core.Registry) error { return nil }

// TestTheDescriptorNamesWhatItDeliversAndRequires checks that the descriptor claims MFA exclusively and requires the capabilities another module delivers.
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

// TestPrepareStatesEnabledAndNothingElse checks that Prepare states the enabled stance and nothing else.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	got, err := Module().Prepare(t.Context(), core.New())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess checks that importing the package registers the module with the process registry.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	got, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
	if len(got.Provides) != 2 || got.Provides[0].Token != core.Token((*authn.MFA)(nil)) {
		t.Errorf("the registered module delivers %+v, want the multi-factor capability", got.Provides)
	}
}

// TestARealRegistryDeliversMultiFactorAndTheAccountLifecycleHook checks that a real registry builds one instance delivering MFA and the account lifecycle hook.
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

// TestTwoFactorsOfOneKindFailTheDrive checks that two factors of one kind fail the drive with ErrFactorConflict.
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

// TestAFactorClaimingAKindOutsideTheSetFailsTheDrive checks that a factor declaring a kind outside the closed set fails the drive.
func TestAFactorClaimingAKindOutsideTheSetFailsTheDrive(t *testing.T) {
	err := assembly{
		factors: []authn.Factor{newFactor(authn.FactorKind("sms-2020"))},
		migrate: true,
	}.drive(t, nothing)
	if !errors.Is(err, authn.ErrInvalidClaim) {
		t.Errorf("the drive returned %v, want ErrInvalidClaim", err)
	}
}

// TestNoFactorWithTheMandatorySwitchOnFailsTheDrive checks that the mandatory switch with no factor module fails the drive with ErrNoFactor.
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

// TestNoFactorWithTheSwitchOffIsALegalAssembly checks that no factor module with the switch off is a legal assembly.
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

// TestStartRefusesWhenTheRegisterTableIsMissing checks that Start refuses with ErrNotMigrated when the register table is missing.
func TestStartRefusesWhenTheRegisterTableIsMissing(t *testing.T) {
	err := assembly{factors: []authn.Factor{newFactor(authn.FactorTOTP)}}.drive(t, func(context.Context, *core.Registry) error {
		t.Error("the drive reached its action with the table missing")
		return nil
	})
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Errorf("the drive returned %v, want ErrNotMigrated", err)
	}
}

// TestStartRefusesWhenAccountsHoldAKindNoModuleDelivers checks that Start refuses when the register holds active factors of a kind no module delivers.
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

// TestStartWarnsWhenTheAdmissionLayerTakesNoPart checks that Start warns when no tenant admission layer enforces a tenant's requirement.
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

// TestStartTakesUndeliveredKindsAsUnavailableWithoutFailing checks that a kind accounts hold without an implementation is reported as unavailable, not as a startup failure.
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

// TestStartSaysWhichFactorKindsTakePart checks the startup line that names the factor kinds taking part.
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
