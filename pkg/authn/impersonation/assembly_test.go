package impersonation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
)

// The stand-in modules an assembly drives: the capabilities another module
// delivers exclusively in a real command.
const (
	dbModule      = "stand-in.db"
	accountModule = "stand-in.account"
	mounterModule = "stand-in.mounter"
	guardModule   = "stand-in.guard"
	sessionModule = "stand-in.session"
	mfaModule     = "stand-in.mfa"
	auditModule   = "stand-in.audit"
)

// standIn is a module that declares the tokens names and delivers instance from
// its New.
func standIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// TestTheDescriptorNamesWhatItDeliversAndRequires holds the descriptor's shape.
func TestTheDescriptorNamesWhatItDeliversAndRequires(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.impersonation" {
		t.Errorf("module name = %q (constant %q), want authn.impersonation", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 || m.Provides[0].Token != core.Token((*authn.Impersonation)(nil)) || !m.Provides[0].Exclusive {
		t.Errorf("Provides = %+v, want the impersonation capability exclusively", m.Provides)
	}

	wantRequired := []core.Token{
		(*db.Database)(nil), (*mount.Mounter)(nil), (*authn.Sessions)(nil), (*authn.Accounts)(nil),
		(*authn.Memberships)(nil), (*authn.MFA)(nil), (*audit.Audit)(nil), (*authn.ScopeGuard)(nil),
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
	if len(m.Requires) != len(wantRequired)+1 || !m.Requires[len(m.Requires)-1].Optional {
		t.Errorf("Requires = %+v, want the settings store as the one optional dependency", m.Requires)
	}
}

// TestPrepareStatesEnabledAndNothingElse holds the stance.
func TestPrepareStatesEnabledAndNothingElse(t *testing.T) {
	got, err := Module().Prepare(t.Context(), core.New())
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
	if len(got.Provides) != 1 || got.Provides[0].Token != core.Token((*authn.Impersonation)(nil)) {
		t.Errorf("the registered module delivers %+v, want the impersonation capability", got.Provides)
	}
}

// assembly is one drive of a real registry through the module's descriptor.
type assembly struct {
	// guard is the scope guard; nil leaves the admission layer out, which must
	// fail the drive with a missing provider.
	guard authn.ScopeGuard
	// migrate builds the module's table before the drive; false leaves it absent
	// so that Start's probe fails.
	migrate bool
}

// drive advances the assembly to Start and returns what the drive returned.
func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()

	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "imp.db"), nil)
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	if a.migrate {
		applyMigrations(t, handle, sqliteDialect)
	}

	mounter := mounttest.New(t)
	accounts := newAccounts()
	sessions := newSessions(true, base)
	memberships := newMemberships()
	mfa := &fakeMFA{seasoned: true}

	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}

	reg.Register(standIn(dbModule, database, (*db.Database)(nil)))
	names = append(names, dbModule)
	reg.Register(standIn(mounterModule, mounter, (*mount.Mounter)(nil)))
	names = append(names, mounterModule)
	reg.Register(standIn(sessionModule, sessions, (*authn.Sessions)(nil)))
	names = append(names, sessionModule)
	reg.Register(standIn(accountModule, accounts, (*authn.Accounts)(nil)))
	names = append(names, accountModule)
	reg.Register(standIn("stand-in.memberships", memberships, (*authn.Memberships)(nil)))
	names = append(names, "stand-in.memberships")
	reg.Register(standIn(mfaModule, mfa, (*authn.MFA)(nil)))
	names = append(names, mfaModule)
	reg.Register(standIn(auditModule, &recordingAudit{}, (*audit.Audit)(nil)))
	names = append(names, auditModule)
	if a.guard != nil {
		reg.Register(standIn(guardModule, a.guard, (*authn.ScopeGuard)(nil)))
		names = append(names, guardModule)
	}
	reg.Register(standIn("stand-in.settings", newStore(), (*settings.Store)(nil)))
	names = append(names, "stand-in.settings")

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	})
}

// TestARealRegistryDeliversImpersonation holds the descriptor end to end.
func TestARealRegistryDeliversImpersonation(t *testing.T) {
	var delivered authn.Impersonation
	err := assembly{
		guard:   fakeScopeGuard{covers: map[string]bool{}},
		migrate: true,
	}.drive(t, func(_ context.Context, reg *core.Registry) error {
		var err error
		delivered, err = core.Resolve[authn.Impersonation](reg)
		return err
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if delivered == nil {
		t.Fatal("the registry delivered nothing for the impersonation capability")
	}
}

// TestTheDriveFailsWithoutAScopeGuard holds the required dependency: without the
// admission layer's scope guard the assembly fails before any instance is built.
func TestTheDriveFailsWithoutAScopeGuard(t *testing.T) {
	err := assembly{migrate: true}.drive(t, func(context.Context, *core.Registry) error { return nil })
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("the drive = %v, want ErrMissingProvider", err)
	}
}

// TestTheDriveFailsWithoutTheTable holds Start's probe.
func TestTheDriveFailsWithoutTheTable(t *testing.T) {
	err := assembly{guard: fakeScopeGuard{covers: map[string]bool{}}}.drive(t, func(context.Context, *core.Registry) error { return nil })
	if !errors.Is(err, authn.ErrNotMigrated) {
		t.Fatalf("the drive = %v, want ErrNotMigrated", err)
	}
}

// TestTheDriveRefusesAGuardedMountEndpoint holds the endpoint check: the routes
// may not be mounted on an endpoint the admission layer guards.
func TestTheDriveRefusesAGuardedMountEndpoint(t *testing.T) {
	err := assembly{
		guard:   fakeScopeGuard{covers: map[string]bool{mounttest.DefaultEndpoint: true}},
		migrate: true,
	}.drive(t, func(context.Context, *core.Registry) error { return nil })
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the drive = %v, want ErrInvalidConfig", err)
	}
}
