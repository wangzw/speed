package sso

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

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

// The stand-in modules an assembly drives: the capabilities another module
// delivers exclusively in a real command, each supplied to a module of its own
// here because the modules that deliver them are not ones these cases bring in.
const (
	dbModule      = "stand-in.db"
	mounterModule = "stand-in.mounter"
	admission     = "tenant.admission"
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

// standInLogger is a logging capability that writes every record to a buffer a
// case reads.
type standInLogger struct{ logs *logBuffer }

func (l standInLogger) Named(string) *slog.Logger { return l.logs.logger() }
func (l standInLogger) Redaction() log.Redaction  { return nil }

// nothing is a drive action that asserts nothing and succeeds.
func nothing(context.Context, *core.Registry) error { return nil }

// assembly is one drive of a real registry through this module's descriptor. The
// database and the mounter are supplied as stand-in modules of their own; what a
// case varies is whether the tables and the root key are there, whether the
// admission layer takes part, and whether a logging capability is present.
type assembly struct {
	// keyless builds the database without a root key.
	keyless bool
	// migrate builds the module's tables before the drive.
	migrate bool
	// noDatabase registers no database stand-in, so the required dependency is
	// missing.
	noDatabase bool
	// withAdmission registers a module named tenant.admission, so the startup
	// diagnostic about its absence is not written.
	withAdmission bool
	// logging, when non-nil, is supplied to a stand-in for the logging module.
	logging log.Logger
}

// drive advances the assembly to Start, runs action there, and returns what the
// drive returned once every Stop and Close has run.
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

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// TestTheDescriptorNamesWhatItDeliversAndRequires holds the descriptor's shape:
// the SSO provider register is claimed exclusively, and the database and the
// mounter are required.
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

// TestStartWarnsWhenTheAdmissionLayerTakesNoPart holds the startup diagnostic: a
// deployment with no tenant admission layer has nobody enforcing a tenant's
// requirement of SSO, and the module says so with a Warn naming that module.
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
