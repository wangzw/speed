package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

func plainSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("opening the SQLite database at %s: %v", path, err)
	}
	pool := poolOf(t, handle)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Errorf("releasing the connection pool at %s: %v", path, err)
		}
	})
	return handle
}

type statementCounter struct{ seen atomic.Int64 }

func (*statementCounter) Name() string { return "statement-counter" }

func (c *statementCounter) Initialize(handle *gorm.DB) error {
	count := func(*gorm.DB) { c.seen.Add(1) }
	if err := handle.Callback().Raw().Before("gorm:raw").Register("statement-counter:raw", count); err != nil {
		return err
	}
	return handle.Callback().Row().Before("gorm:row").Register("statement-counter:row", count)
}

func counted(t *testing.T, handle *gorm.DB) *statementCounter {
	t.Helper()
	counter := &statementCounter{}
	if err := handle.Use(counter); err != nil {
		t.Fatalf("installing the statement counter: %v", err)
	}
	return counter
}

func sectionOf(spec Spec, dsn string) string {
	body := fmt.Sprintf(`{"dsn":%q}`, dsn)
	parts := strings.Split(spec.ConfigNamespace, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		body = fmt.Sprintf(`{%q:%s}`, parts[i], body)
	}
	return body
}

func builtByNew(t *testing.T, spec Spec, dsn string, modules ...core.Module) *database {
	t.Helper()
	module := NewModule(spec)
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(module)
	for _, m := range modules {
		reg.Register(m)
	}
	reg.Supply(readerFor(t, spec, sectionOf(spec, dsn)), config.ModuleName)

	built, err := module.New(t.Context(), reg)
	if err != nil {
		t.Fatalf("running the New of module %s over %s: %v", spec.ModuleName, dsn, err)
	}
	product, ok := built.(*database)
	if !ok {
		t.Fatalf("the New of module %s built a %T, want its own product", spec.ModuleName, built)
	}
	t.Cleanup(func() {
		if err := Close(product.handle); err != nil {
			t.Errorf("releasing the pool of the product built over %s: %v", dsn, err)
		}
	})
	return product
}

// rivalSpec is another implementation's Spec: the same engine under another
// name, which is what makes its product another implementation's.
func rivalSpec() Spec {
	spec := sqliteSpec()
	spec.ModuleName, spec.ConfigNamespace = "db.rival", "db.rival"
	return spec
}

// driveCatching drives a registry and turns a panic out of it into a failure of
// the case, instead of the abort of the whole test binary: a panic in a rollback
// is what several of these cases exist to catch, and they would take the rest of
// the run down with them.
func driveCatching(t *testing.T, reg *core.Registry, drive core.Drive) error {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("the drive panicked: %v", recovered)
		}
	}()
	return reg.Drive(t.Context(), drive)
}

func declaredOn(handle *gorm.DB) []string {
	names := make([]string, 0, len(handle.Plugins))
	for name := range handle.Plugins {
		if name == openedMarkerName || name == engineMarkerName {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func pluginDeclarer(name string, plugin gorm.Plugin) core.Module {
	return core.Module{Name: name, Resources: []any{Plugin{Plugin: plugin}}}
}

// TestMigrateRefusesAnInstanceItsOwnNewDidNotBuild is the refusal itself, driven the way a host
// meets it: an implementation of Database the host wrote, supplied for this module.
func TestMigrateRefusesAnInstanceItsOwnNewDidNotBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	handle := plainSQLite(t, path)
	counter := counted(t, handle)

	lock := &countingLock{}
	mutexesBuilt := 0
	spec := probeSpec()
	spec.NewMigrationLock = func(time.Duration) MigrationLock { mutexesBuilt++; return lock }
	spec.DefaultMigrationLockTimeout = time.Minute

	stub := &stubDatabase{handle: handle, dialect: SQLite}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(NewModule(spec))
	reg.Register(declaring("catalogue", migrationSet(SQLite, map[string]string{
		"0001_widgets.sql": createWidgets,
	})))
	reg.Supply(readerFor(t, spec, `{}`), config.ModuleName)
	reg.Supply(stub, probeNamespace)

	err := driveCatching(t, reg, core.Drive{Depth: core.StageMigrate, Participants: []string{"catalogue"}})
	if !errors.Is(err, ErrForeignInstance) {
		t.Errorf("a drive that reached Migrate with the host's own implementation supplied ended with "+
			"%v, want ErrForeignInstance", err)
	}
	if errors.Is(err, ErrMigrationFailed) || errors.Is(err, ErrMigrationLockTimeout) {
		t.Errorf("the refusal is also a migration failure, which sends the host to migration files "+
			"that have nothing wrong with them: %v", err)
	}
	if err != nil {
		for _, want := range []string{probeNamespace, fmt.Sprintf("%T", stub), "No migration was applied"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not say %q", err, want)
			}
		}
	}

	if issued := counter.seen.Load(); issued != 0 {
		t.Errorf("%d statements were issued on the supplied handle before the refusal, want none", issued)
	}
	if mutexesBuilt != 0 || lock.acquires != 0 {
		t.Errorf("the mutex was built %d times and taken %d, want it left alone: the instance is "+
			"judged before the run begins", mutexesBuilt, lock.acquires)
	}
	reopened := plainSQLite(t, path)
	for _, table := range []string{"widgets", recordTableName} {
		if hasTable(t, reopened, table) {
			t.Errorf("table %q exists in the database behind the refused instance", table)
		}
	}
	if err := poolOf(t, handle).Ping(); err == nil {
		t.Errorf("the pool behind the refused instance still answers after the rollback, so " +
			"nothing released it")
	}
}

// TestTheRefusalIsTheDrivesResultWhenTheStandInCannotGiveItsHandle drives to Migrate over a stand-
// in supplied for the module that has no handle to release.
func TestTheRefusalIsTheDrivesResultWhenTheStandInCannotGiveItsHandle(t *testing.T) {
	for _, c := range []struct {
		name     string
		instance any
	}{
		{"a stand-in embedding a nil Database, by value", partialDatabase{}},
		{"a stand-in embedding a nil Database, by pointer", &partialDatabase{}},
		{"an implementation of Database whose DB panics", panickingDatabase{}},
		{"an implementation of Database whose handle is a zero value", &stubDatabase{handle: &gorm.DB{}}},
		{"an implementation of Database whose handle has no pool", &stubDatabase{handle: &gorm.DB{Config: &gorm.Config{}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := probeSpec()
			reg := core.New()
			reg.Register(config.Module())
			reg.Register(NewModule(spec))
			reg.Register(declaring("catalogue", migrationSet(SQLite, map[string]string{
				"0001_widgets.sql": createWidgets,
			})))
			reg.Supply(readerFor(t, spec, `{}`), config.ModuleName)
			reg.Supply(c.instance, spec.ModuleName)

			err := driveCatching(t, reg, core.Drive{
				Depth: core.StageMigrate, Participants: []string{"catalogue"},
			})
			if !errors.Is(err, ErrForeignInstance) {
				t.Fatalf("a drive that reached Migrate with %s supplied ended with %v, want ErrForeignInstance",
					c.name, err)
			}
			if want := fmt.Sprintf("%T", c.instance); !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal %q does not name the instance's type %s", err, want)
			}
			if failed := fmt.Sprintf("module %q Close", spec.ModuleName); strings.Contains(err.Error(), failed) {
				t.Errorf("the rollback added a failure of its own to the refusal: %v", err)
			}
		})
	}
}

// TestMigrateRefusesEveryShapeOfInstanceItsOwnNewDidNotBuild holds the check to each kind of
// instance that is not this module's own product.
func TestMigrateRefusesEveryShapeOfInstanceItsOwnNewDidNotBuild(t *testing.T) {
	spec := sqliteSpec()
	rival := rivalSpec()
	other := builtByNew(t, rival, filepath.Join(t.TempDir(), "rival.db"))

	for _, c := range []struct {
		name     string
		instance any
		mention  string
	}{
		{"the host's own implementation of Database", &stubDatabase{dialect: SQLite}, ""},
		{"a typed nil of the host's own implementation", (*stubDatabase)(nil), ""},
		{"no instance at all", nil, ""},
		{"a value that is not a Database", struct{ other int }{}, ""},
		{"a nil product of this package's own type", (*database)(nil), ""},
		{"the product of another implementation's New", other, rival.ModuleName},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := NewModule(spec).Migrate(t.Context(), core.New(), c.instance)
			if !errors.Is(err, ErrForeignInstance) {
				t.Fatalf("Migrate on %s gave %v, want ErrForeignInstance", c.name, err)
			}
			if errors.Is(err, ErrMigrationFailed) {
				t.Errorf("the refusal of %s is also a migration failure: %v", c.name, err)
			}
			for _, want := range []string{spec.ModuleName, fmt.Sprintf("%T", c.instance), c.mention} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal of %s does not say %q: %v", c.name, want, err)
				}
			}
		})
	}
}

// TestMigrateAcceptsAnInstanceItsOwnNewBuilt checks that an instance this module's own New built in
// a registry of the host's making is accepted.
func TestMigrateAcceptsAnInstanceItsOwnNewBuilt(t *testing.T) {
	spec := sqliteSpec()
	path := filepath.Join(t.TempDir(), "built.db")
	product := builtByNew(t, spec, path)
	counter := counted(t, product.handle)

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(NewModule(spec))
	reg.Register(declaring("catalogue", migrationSet(SQLite, map[string]string{
		"0001_widgets.sql": createWidgets,
	})))
	reg.Supply(readerFor(t, spec, `{}`), config.ModuleName)
	reg.Supply(product, spec.ModuleName)

	if err := driveCatching(t, reg, core.Drive{
		Depth: core.StageMigrate, Participants: []string{"catalogue"},
	}); err != nil {
		t.Fatalf("a drive over an instance the module's own New built ended with %v", err)
	}

	if counter.seen.Load() == 0 {
		t.Error("the migration run issued nothing on the handle of the instance it was handed")
	}
	reopened := plainSQLite(t, path)
	if !hasTable(t, reopened, "widgets") {
		t.Error("the declared migration was not applied to the database behind the supplied instance")
	}
	if got := recordedMigrations(t, reopened); len(got) != 1 {
		t.Errorf("the record table holds %v, want the one migration that was applied", got)
	}
}

// TestTheCheckJudgesTheOriginAndNotThePluginsOnTheHandle pins that the check does
// not compare the plugins on the handle with what the run declares.
func TestTheCheckJudgesTheOriginAndNotThePluginsOnTheHandle(t *testing.T) {
	spec := sqliteSpec()
	product := builtByNew(t, spec, filepath.Join(t.TempDir(), "built.db"),
		pluginDeclarer("audit.built", silentPlugin("built-with")))

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(NewModule(spec))
	reg.Register(pluginDeclarer("audit.run", silentPlugin("declared-by-the-run")))
	reg.Supply(readerFor(t, spec, `{}`), config.ModuleName)
	reg.Supply(product, spec.ModuleName)

	if err := driveCatching(t, reg, core.Drive{
		Depth: core.StageMigrate, Participants: []string{"audit.run"},
	}); err != nil {
		t.Fatalf("an instance whose plugins differ from what the run declares was refused: %v", err)
	}
	if got, want := declaredOn(product.handle), []string{"built-with"}; !slices.Equal(got, want) {
		t.Errorf("the supplied handle carries the plugins %v after the drive, want %v: what the New "+
			"installed, and nothing the run declares", got, want)
	}
}

// TestADriveThatStopsBeforeMigrateDoesNotJudgeTheInstance pins that an instance the
// module's New did not build is refused at Migrate and not before.
func TestADriveThatStopsBeforeMigrateDoesNotJudgeTheInstance(t *testing.T) {
	handle := plainSQLite(t, filepath.Join(t.TempDir(), "foreign.db"))
	stub := &stubDatabase{handle: handle, dialect: SQLite}
	reg := core.New()
	reg.Register(NewModule(probeSpec()))
	reg.Supply(stub, probeNamespace)

	var resolved Database
	err := driveCatching(t, reg, core.Drive{
		Depth: core.StageNew,
		Action: func(_ context.Context, reg *core.Registry) error {
			taken, err := core.Resolve[Database](reg)
			resolved = taken
			return err
		},
	})
	if err != nil {
		t.Fatalf("a drive that stopped at New ended with %v, want it to end without an error", err)
	}
	if resolved != Database(stub) {
		t.Errorf("the action resolved %v, want the instance that was supplied", resolved)
	}
	if err := poolOf(t, handle).Ping(); err == nil {
		t.Error("the pool behind the supplied instance still answers after the drive ended")
	}
}

// TestAPlaceholderModuleCarriesAStandInThroughAnyDepth backs the way out the refusal names.
func TestAPlaceholderModuleCarriesAStandInThroughAnyDepth(t *testing.T) {
	const placeholder = "db.standin"
	stub := &stubDatabase{dialect: SQLite}

	var taken Database
	dependant := core.Module{
		Name:     "dependant",
		Requires: []core.Requirement{{Token: (*Database)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			resolved, err := core.Resolve[Database](reg)
			taken = resolved
			return err
		},
	}
	reg := core.New()
	reg.Register(core.Module{Name: placeholder, Provides: []core.Provision{{Token: (*Database)(nil)}}})
	reg.Register(dependant)
	reg.Supply(stub, placeholder)

	if err := driveCatching(t, reg, core.Drive{
		Depth: core.StageInit, Participants: []string{"dependant"},
	}); err != nil {
		t.Fatalf("a drive over a placeholder module carrying the stand-in ended with %v", err)
	}
	if taken != Database(stub) {
		t.Errorf("the dependant took up %v, want the stand-in that was supplied", taken)
	}
}

// TestARollbackBeforeMigrateClosesWhateverWasSupplied drives to an end before Migrate in the two
// ways one can — a failure in another module's Prepare.
func TestARollbackBeforeMigrateClosesWhateverWasSupplied(t *testing.T) {
	failure := errors.New("the module that stopped the startup")
	failing := core.Module{
		Name: "failing",
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{}, failure
		},
	}

	for _, c := range []struct {
		name  string
		build func(t *testing.T) (instance any, handle *gorm.DB)
	}{
		{"a typed nil of the host's own implementation", func(*testing.T) (any, *gorm.DB) {
			return (*stubDatabase)(nil), nil
		}},
		{"an implementation of Database that reports no handle", func(*testing.T) (any, *gorm.DB) {
			return &stubDatabase{dialect: SQLite}, nil
		}},
		{"a stand-in embedding a nil Database", func(*testing.T) (any, *gorm.DB) {
			return partialDatabase{}, nil
		}},
		{"an implementation of Database whose DB panics", func(*testing.T) (any, *gorm.DB) {
			return panickingDatabase{}, nil
		}},
		{"an implementation of Database whose handle is a zero value", func(*testing.T) (any, *gorm.DB) {
			return &stubDatabase{handle: &gorm.DB{}}, nil
		}},
		{"an implementation of Database whose handle has no pool", func(*testing.T) (any, *gorm.DB) {
			return &stubDatabase{handle: &gorm.DB{Config: &gorm.Config{}}}, nil
		}},
		{"an implementation of Database over a live pool", func(t *testing.T) (any, *gorm.DB) {
			handle := plainSQLite(t, filepath.Join(t.TempDir(), "foreign.db"))
			return &stubDatabase{handle: handle, dialect: SQLite}, handle
		}},
		{"the product of another implementation's New", func(t *testing.T) (any, *gorm.DB) {
			product := builtByNew(t, rivalSpec(), filepath.Join(t.TempDir(), "rival.db"))
			return product, product.handle
		}},
	} {
		for _, drive := range []struct {
			name  string
			drive core.Drive
			want  error
		}{
			{
				"the drive fails in another module's Prepare",
				core.Drive{Depth: core.StageServe, Participants: []string{"failing"}},
				failure,
			},
			{"the drive stops at Prepare", core.Drive{Depth: core.StagePrepare}, nil},
		} {
			t.Run(c.name+" when "+drive.name, func(t *testing.T) {
				instance, handle := c.build(t)
				reg := core.New()
				reg.Register(NewModule(probeSpec()))
				reg.Register(failing)
				reg.Supply(instance, probeNamespace)

				err := driveCatching(t, reg, drive.drive)
				if drive.want == nil {
					if err != nil {
						t.Fatalf("the drive ended with %v, want it to end without an error", err)
					}
				} else if !errors.Is(err, drive.want) {
					t.Fatalf("the drive ended with %v, want the failure that started it", err)
				}
				if err != nil && strings.Contains(err.Error(), "Close") {
					t.Errorf("the rollback added a failure of its own to the one that started it: %v", err)
				}
				if handle != nil {
					if err := poolOf(t, handle).Ping(); err == nil {
						t.Error("the pool behind the supplied instance still answers after the rollback")
					}
				}
			})
		}
	}
}
