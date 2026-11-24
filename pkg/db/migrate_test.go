package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

//go:embed testdata/assets
var unrelatedAssets embed.FS

const createWidgets = `CREATE TABLE widgets (id INTEGER NOT NULL, PRIMARY KEY (id))`

func forEachDialect(t *testing.T, run func(t *testing.T, dialect Dialect, handle *gorm.DB)) {
	t.Helper()
	for _, fixture := range dbsuite.Fixtures(t) {
		t.Run(fixture.Dialect, func(t *testing.T) {
			run(t, Dialect(fixture.Dialect), fixture.Open(t))
		})
	}
}

func migrationSet(dialect Dialect, files map[string]string) Migrations {
	return Migrations{FS: filesUnder(string(dialect), files)}
}

func filesUnder(dir string, files map[string]string) fstest.MapFS {
	tree := fstest.MapFS{}
	for name, body := range files {
		tree[path.Join(dir, name)] = &fstest.MapFile{Data: []byte(body)}
	}
	return tree
}

func declaring(name string, sets ...Migrations) core.Module {
	resources := make([]any, 0, len(sets))
	for _, set := range sets {
		resources = append(resources, set)
	}
	return core.Module{Name: name, Resources: resources}
}

func registryOf(modules ...core.Module) *core.Registry {
	reg := core.New()
	for _, m := range modules {
		reg.Register(m)
	}
	return reg
}

func settledRegistry(t *testing.T, modules ...core.Module) *core.Registry {
	t.Helper()
	reg := registryOf(modules...)
	names := make([]string, 0, len(modules))
	for _, m := range reg.Modules() {
		names = append(names, m.Name)
	}
	if err := reg.Drive(t.Context(), core.Drive{Participants: names}); err != nil {
		t.Fatalf("settling the sets of %v: %v", names, err)
	}
	return reg
}

func hasTable(t *testing.T, handle *gorm.DB, name string) bool {
	t.Helper()
	return handle.Migrator().HasTable(name)
}

type recordRow struct {
	Module string
	File   string
}

func recordedMigrations(t *testing.T, handle *gorm.DB) []recordRow {
	t.Helper()
	var rows []recordRow
	if err := handle.Raw(selectRecords).Scan(&rows).Error; err != nil {
		t.Fatalf("reading the migration record table: %v", err)
	}
	return rows
}

// TestMigrationExecutionAndRecordShareOneTransaction is the observation that tells a one-
// transaction implementation apart from one that executes first and records second.
func TestMigrationExecutionAndRecordShareOneTransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		blocked := fmt.Sprintf(`CREATE TABLE %s (
			module TEXT NOT NULL,
			file TEXT NOT NULL,
			applied_at TIMESTAMP NOT NULL,
			extra TEXT NOT NULL,
			PRIMARY KEY (module, file)
		)`, recordTableName)
		if err := handle.Exec(blocked).Error; err != nil {
			t.Fatalf("preparing the record table that refuses inserts: %v", err)
		}

		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, nil)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("applying a migration whose record cannot be written reported %v, want %v",
				err, ErrMigrationFailed)
		}
		if hasTable(t, handle, "widgets") {
			t.Error("the widgets table survived a migration whose record was never written: " +
				"the execution and the record are not in one transaction, and the next start " +
				"will fail on an object that already exists")
		}
		if rows := recordedMigrations(t, handle); len(rows) != 0 {
			t.Errorf("the record table holds %v after a failed migration, want nothing", rows)
		}
	})
}

// TestSecondRunAppliesNothing pins idempotence.
func TestSecondRunAppliesNothing(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("first run: %v", err)
		}
		first := recordedMigrations(t, handle)
		if len(first) != 1 {
			t.Fatalf("the first run recorded %v, want one migration", first)
		}

		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("second run: %v, want the already-applied migration to be skipped", err)
		}
		if second := recordedMigrations(t, handle); len(second) != len(first) {
			t.Errorf("the second run left %v in the record table, want the %v the first run wrote",
				second, first)
		}
	})
}

type mutedCapability interface{ muted() }

type rivalProduct struct{}

func (rivalProduct) muted() {}

// TestDisabledModuleMigrationsAreNotApplied pins the enablement filter.
func TestDisabledModuleMigrationsAreNotApplied(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		muted := declaring("muted", migrationSet(dialect, map[string]string{
			"0001_create_muted.sql": `CREATE TABLE muted_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
		}))
		// The muted module loses a resolution rather than stating that it does not run: a module taken in at run level has to start, so a case that needs the disabled verdict has to produce it the one way it exists.
		muted.Provides = []core.Provision{{Token: (*mutedCapability)(nil), Exclusive: true}}
		rival := core.Module{
			Name:     "rival",
			Provides: []core.Provision{{Token: (*mutedCapability)(nil), Exclusive: true}},
			Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
				return core.Enablement{State: core.StateEnabled}, nil
			},
			New: func(context.Context, *core.Registry) (any, error) { return rivalProduct{}, nil },
		}
		catalog := declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		}))

		if err := startupWith(t, handle, dialect, muted, rival, catalog); err != nil {
			t.Fatalf("startup: %v", err)
		}
		if hasTable(t, handle, "muted_rows") {
			t.Error("a module resolution stood down had its table created")
		}
		if !hasTable(t, handle, "widgets") {
			t.Error("an enabled module's table was not created, so the filter caught too much")
		}
	})
}

// TestAnUnincludedModuleMigrationsAreNotApplied pins the other half of the filter: the
// participating set of the command that drives the run.
func TestAnUnincludedModuleMigrationsAreNotApplied(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		outsider := declaring("outsider", migrationSet(dialect, map[string]string{
			"0001_create_outsider.sql": `CREATE TABLE outsider_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
		}))
		catalog := declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		}))

		if err := driveWith(t, handle, dialect, []string{"catalog"}, outsider, catalog); err != nil {
			t.Fatalf("startup: %v", err)
		}
		if hasTable(t, handle, "outsider_rows") {
			t.Error("the migration of a module this command did not take in was applied")
		}
		for _, row := range recordedMigrations(t, handle) {
			if row.Module == "outsider" {
				t.Errorf("the record table holds %+v, and this command did not take that module in", row)
			}
		}
		if !hasTable(t, handle, "widgets") {
			t.Error("an included module's table was not created, so the filter caught too much")
		}
	})
}

type silentPlugin string

func (p silentPlugin) Name() string              { return string(p) }
func (p silentPlugin) Initialize(*gorm.DB) error { return nil }

// TestAModuleReachedForItsDeclarationsIsMigratedWithoutRunning pins the split between what a
// command runs and whose declarations it collects.
func TestAModuleReachedForItsDeclarationsIsMigratedWithoutRunning(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		built := false
		outsider := declaring("outsider", migrationSet(dialect, map[string]string{
			"0001_create_outsider.sql": `CREATE TABLE outsider_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
		}))
		outsider.New = func(context.Context, *core.Registry) (any, error) {
			built = true
			return nil, nil
		}

		if err := driveSpec(t, handle, dialect, []string{},
			[]core.Declaration{{Name: "outsider"}}, outsider); err != nil {
			t.Fatalf("startup: %v", err)
		}
		if !hasTable(t, handle, "outsider_rows") {
			t.Error("a module reached for its declarations was not migrated, so this command " +
				"applied nothing and reported success")
		}
		var recorded bool
		for _, row := range recordedMigrations(t, handle) {
			if row.Module == "outsider" {
				recorded = true
			}
		}
		if !recorded {
			t.Errorf("the record table holds %v and nothing for the outsider, so its file was "+
				"applied without being recorded, or not applied at all",
				recordedMigrations(t, handle))
		}
		if built {
			t.Error("a module this command reached for its declarations alone was constructed, " +
				"so the two levels are not the difference they are meant to be")
		}
	})
}

// TestADeclarationLevelModuleKeepsItsPluginOutOfTheRun pins the one place the two collections
// disagree, on the module shape where it shows: one that both owns a table and declares a plugin.
func TestADeclarationLevelModuleKeepsItsPluginOutOfTheRun(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		outsider := declaring("outsider", migrationSet(dialect, map[string]string{
			"0001_create_outsider.sql": `CREATE TABLE outsider_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
		}))
		outsider.Resources = append(outsider.Resources, Plugin{Plugin: silentPlugin("outsider")})

		reg := registryOf(outsider)
		if err := reg.Drive(t.Context(), core.Drive{
			DeclaringOnly: []core.Declaration{{Name: "outsider"}},
		}); err != nil {
			t.Fatalf("settling the sets: %v", err)
		}
		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("applying migrations: %v", err)
		}
		if !hasTable(t, handle, "outsider_rows") {
			t.Error("the declaring set did not reach the table of a module this command " +
				"reached for its declarations")
		}

		installs, err := collectPlugins(reg)
		if err != nil {
			t.Fatalf("collecting plugins: %v", err)
		}
		for _, install := range installs {
			if install.module == "outsider" {
				t.Errorf("the plugin of a module outside this run was collected for installation: "+
					"%+v, on a module that will never issue a statement", install)
			}
		}
	})
}

// TestAllResourcesPutsADeclarationLevelModulesPluginInForce pins the host's override of the
// collection's own domain.
func TestAllResourcesPutsADeclarationLevelModulesPluginInForce(t *testing.T) {
	outsider := declaring("outsider")
	outsider.Resources = append(outsider.Resources, Plugin{Plugin: silentPlugin("outsider")})

	reg := registryOf(outsider)
	if err := reg.Drive(t.Context(), core.Drive{
		DeclaringOnly: []core.Declaration{{Name: "outsider", AllResources: true}},
	}); err != nil {
		t.Fatalf("settling the sets: %v", err)
	}

	installs, err := collectPlugins(reg)
	if err != nil {
		t.Fatalf("collecting plugins: %v", err)
	}
	if len(installs) != 1 || installs[0].module != "outsider" {
		t.Errorf("collecting the plugins of a module whose declarations are in force gave %+v, "+
			"want the one it declares", installs)
	}
}

// TestOnlyCurrentDialectSubdirectoryIsRead pins that a declaration covering several engines
// contributes only the running one's files.
func TestOnlyCurrentDialectSubdirectoryIsRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		tree := fstest.MapFS{}
		for _, other := range []Dialect{Postgres, SQLite, "mysql"} {
			body := createWidgets
			if other != dialect {
				body = `THIS IS NOT SQL IN ANY DIALECT`
			}
			tree[path.Join(string(other), "0001_create_widgets.sql")] = &fstest.MapFile{Data: []byte(body)}
		}

		reg := settledRegistry(t, declaring("catalog", Migrations{FS: tree}))
		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("applying the %s files of a multi-dialect declaration: %v", dialect, err)
		}
		if !hasTable(t, handle, "widgets") {
			t.Errorf("the %s subdirectory was not applied", dialect)
		}
	})
}

// TestMissingDialectSubdirectoryIsZeroMigrations pins that a module which does not support the
// running engine says so by leaving the subdirectory out, and that this is not a failure.
func TestMissingDialectSubdirectoryIsZeroMigrations(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog", migrationSet("mysql", map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("a declaration without a %s subdirectory reported %v, want no error", dialect, err)
		}
		if hasTable(t, handle, "widgets") {
			t.Error("a subdirectory belonging to another dialect was applied")
		}
		if rows := recordedMigrations(t, handle); len(rows) != 0 {
			t.Errorf("the record table holds %v, want nothing", rows)
		}
	})
}

// TestUnrelatedEmbedFSIsNotApplied is the end-to-end form of why Migrations is a struct rather than
// an fs.FS.
func TestUnrelatedEmbedFSIsNotApplied(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		assetRoot, err := fs.Sub(unrelatedAssets, "testdata/assets")
		if err != nil {
			t.Fatalf("reaching the embedded assets: %v", err)
		}

		reg := settledRegistry(t,
			core.Module{Name: "i18n", Resources: []any{unrelatedAssets}},
			core.Module{Name: "templates", Resources: []any{assetRoot}},
			declaring("catalog", migrationSet(dialect, map[string]string{
				"0001_create_widgets.sql": createWidgets,
			})),
		)

		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("applying migrations alongside unrelated embedded assets: %v", err)
		}
		if !hasTable(t, handle, "widgets") {
			t.Fatal("the declared migration was not applied, so this case proves nothing about the rest")
		}
		if hasTable(t, handle, "unrelated_asset_table") {
			t.Error("an embedded asset that was never declared as a migration set was applied")
		}
		for _, row := range recordedMigrations(t, handle) {
			if row.Module != "catalog" {
				t.Errorf("the record table holds %v, declared by no migration set", row)
			}
		}
	})
}

// TestMigrationFailureNamesModuleAndFile pins what the error has to carry: the module is who has to
// change the statement, and the file is which one.
func TestMigrationFailureNamesModuleAndFile(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		const broken = `THIS IS NOT SQL IN ANY DIALECT`
		reference := handle.Exec(broken).Error
		if reference == nil {
			t.Fatal("the fixture accepted a statement that is not SQL, so there is no failure to inspect")
		}

		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0007_broken.sql": broken,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, nil)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a migration that does not parse reported %v, want %v", err, ErrMigrationFailed)
		}
		for _, want := range []string{"catalog", "0007_broken.sql"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error text %q does not name %q", err, want)
			}
		}
		carried := false
		for _, cause := range allErrors(err) {
			if cause.Error() == reference.Error() {
				carried = true
			}
		}
		if !carried {
			t.Errorf("the error %v does not carry the driver's own error, only its text: every %%w "+
				"along the way has to stay one, and a %%v leaves the message all but unchanged "+
				"while errors.Is and errors.As stop reaching what the driver said", err)
		}
	})
}

// TestRecordTableFailureIsAlsoErrMigrationFailed pins the second trigger of the sentinel.
func TestRecordTableFailureIsAlsoErrMigrationFailed(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		unusable := fmt.Sprintf(`CREATE TABLE %s (module TEXT NOT NULL)`, recordTableName)
		if err := handle.Exec(unusable).Error; err != nil {
			t.Fatalf("preparing an unusable record table: %v", err)
		}

		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, nil)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("an unreadable record table reported %v, want %v", err, ErrMigrationFailed)
		}
		if !strings.Contains(err.Error(), recordTableName) {
			t.Errorf("the error text %q does not name the record table, and there is no migration to name", err)
		}
		if hasTable(t, handle, "widgets") {
			t.Error("a migration was applied although the record table could not be read")
		}
	})
}

type probeCapability interface{ probe() }

// TestModulesMigrateInDependencyOrder pins that the order comes from the Requires declarations
// rather than from the module names.
func TestModulesMigrateInDependencyOrder(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		zeta := declaring("zeta", migrationSet(dialect, map[string]string{
			"0001_create_zeta_rows.sql": `CREATE TABLE zeta_rows (id INTEGER NOT NULL, PRIMARY KEY (id))`,
		}))
		zeta.Provides = []core.Provision{{Token: (*probeCapability)(nil)}}

		alpha := declaring("alpha", migrationSet(dialect, map[string]string{
			"0001_fill_zeta_rows.sql": `INSERT INTO zeta_rows (id) VALUES (1)`,
		}))
		alpha.Requires = []core.Requirement{{Token: (*probeCapability)(nil)}}

		reg := settledRegistry(t, alpha, zeta)
		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("applying in dependency order: %v — a dependant's migration ran before the "+
				"module it depends on", err)
		}

		var ids []int64
		if err := handle.Raw(`SELECT id FROM zeta_rows`).Scan(&ids).Error; err != nil {
			t.Fatalf("reading back what the dependant's migration wrote: %v", err)
		}
		if len(ids) != 1 || ids[0] != 1 {
			t.Errorf("zeta_rows holds %v, want the single row alpha's migration inserted", ids)
		}
	})
}

// TestFileOrderWithinAModuleIsLexical pins that file name is the whole of the order inside one
// module: the second file depends on the first having run.
func TestFileOrderWithinAModuleIsLexical(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0002_fill_widgets.sql":   `INSERT INTO widgets (id) VALUES (1)`,
			"0001_create_widgets.sql": createWidgets,
		})))

		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("applying a module's files in name order: %v", err)
		}
		rows := recordedMigrations(t, handle)
		if len(rows) != 2 || rows[0].File != "0001_create_widgets.sql" {
			t.Errorf("the record table holds %v, want the two files in name order", rows)
		}
	})
}

// TestDuplicateFileNameWithinOneModuleIsRejected pins the hole the record table's key leaves open.
func TestDuplicateFileNameWithinOneModuleIsRejected(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog",
			migrationSet(dialect, map[string]string{"0001_create.sql": createWidgets}),
			migrationSet(dialect, map[string]string{"0001_create.sql": `CREATE TABLE gadgets (id INTEGER)`}),
		))

		err := applyMigrations(t.Context(), reg, handle, dialect, nil)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("two declarations of the same file name reported %v, want %v", err, ErrMigrationFailed)
		}
		for _, want := range []string{"catalog", "0001_create.sql"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error text %q does not name %q", err, want)
			}
		}
		if hasTable(t, handle, "widgets") {
			t.Error("the first of the two declarations was applied before the duplicate was caught: " +
				"the clash has to be found while the set is collected, not by the record table's key")
		}
		if rows := recordedMigrations(t, handle); len(rows) != 0 {
			t.Errorf("the record table holds %v although the declaration was rejected", rows)
		}
	})
}

// TestDirectoryInsideADialectDirectoryIsRejected pins that nothing is passed over without a word.
func TestDirectoryInsideADialectDirectoryIsRejected(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql":         createWidgets,
			"archive/0002_create_gadgets.sql": `CREATE TABLE gadgets (id INTEGER)`,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, nil)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a directory among the migration files reported %v, want %v", err, ErrMigrationFailed)
		}
		if !strings.Contains(err.Error(), "archive") {
			t.Errorf("the error text %q does not name the directory that was not applied", err)
		}
		assertNothingWasApplied(t, handle)
	})
}

// TestAFileThatIsNotSQLIsRejected pins the other half of the same rule.
func TestAFileThatIsNotSQLIsRejected(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
			"NOTES.txt":               "this directory holds the widget migrations",
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, nil)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("an entry that is not a migration reported %v, want %v", err, ErrMigrationFailed)
		}
		if !strings.Contains(err.Error(), "NOTES.txt") {
			t.Errorf("the error text %q does not name the entry that was not applied", err)
		}
		assertNothingWasApplied(t, handle)
	})
}

func assertNothingWasApplied(t *testing.T, handle *gorm.DB) {
	t.Helper()
	if hasTable(t, handle, "widgets") {
		t.Error("the legal migration standing beside the rejected entry was applied: the directory has " +
			"to be refused while it is read, not part-way through the run")
	}
	if hasTable(t, handle, recordTableName) {
		if rows := recordedMigrations(t, handle); len(rows) != 0 {
			t.Errorf("the record table holds %v although the declaration was rejected", rows)
		}
	}
}

// TestTheDialectPutsDDLInsideATransaction pins the admission condition every implementation
// subpackage has to meet.
func TestTheDialectPutsDDLInsideATransaction(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		rollback := errors.New("rolled back on purpose")
		err := handle.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(createWidgets).Error; err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("the transaction reported %v, want the error it was rolled back with", err)
		}
		if hasTable(t, handle, "widgets") {
			t.Errorf("%s left a table created inside a rolled-back transaction behind: it does not "+
				"put DDL inside transactions, and a migration's execution cannot be atomic with its "+
				"record on it", dialect)
		}
	})
}

type probeLock struct {
	handle     *gorm.DB
	acquireErr error
	releaseErr error

	acquires             int
	releases             int
	recordTableAtAcquire bool
	recordsAtRelease     int
}

func (l *probeLock) Acquire(ctx context.Context, conn *sql.Conn) error {
	l.acquires++
	if l.acquireErr != nil {
		return l.acquireErr
	}
	l.recordTableAtAcquire = pinned(ctx, l.handle, conn).Migrator().HasTable(recordTableName)
	return nil
}

func (l *probeLock) Release(ctx context.Context, conn *sql.Conn) error {
	l.releases++
	session := pinned(ctx, l.handle, conn)
	if session.Migrator().HasTable(recordTableName) {
		var rows []recordRow
		if err := session.Raw(selectRecords).Scan(&rows).Error; err == nil {
			l.recordsAtRelease = len(rows)
		}
	}
	return l.releaseErr
}

var _ MigrationLock = (*probeLock)(nil)

// TestTheMutexSpansTheWholeRun pins where the mutex is taken and released.
func TestTheMutexSpansTheWholeRun(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{handle: handle}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
			"0002_fill_widgets.sql":   `INSERT INTO widgets (id) VALUES (1)`,
		})))

		if err := applyMigrations(t.Context(), reg, handle, dialect, lock); err != nil {
			t.Fatalf("applying under a mutex: %v", err)
		}
		if lock.acquires != 1 || lock.releases != 1 {
			t.Errorf("the mutex was taken %d times and released %d, want once each: it covers the "+
				"whole run, not one migration at a time", lock.acquires, lock.releases)
		}
		if lock.recordTableAtAcquire {
			t.Error("the record table already existed when the mutex was taken: the run started " +
				"before the mutex, which is the window several replicas collide in")
		}
		if lock.recordsAtRelease != 2 {
			t.Errorf("%d migrations were recorded when the mutex was released, want 2: it was given "+
				"up before the run finished", lock.recordsAtRelease)
		}
	})
}

// TestTheMutexIsReleasedAfterAFailedMigration pins the other end of the span.
func TestTheMutexIsReleasedAfterAFailedMigration(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{handle: handle}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_broken.sql": `THIS IS NOT SQL IN ANY DIALECT`,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, lock)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a failing migration under a mutex reported %v, want %v", err, ErrMigrationFailed)
		}
		if lock.releases != 1 {
			t.Errorf("the mutex was released %d times after a failed migration, want once", lock.releases)
		}
	})
}

// TestReleaseFailureIsReported pins that a mutex left held is not swallowed: it blocks every other
// replica, and a run that reported success would leave the host with nothing to go on.
func TestReleaseFailureIsReported(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{handle: handle, releaseErr: errors.New("the connection went away")}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, lock)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a mutex that would not be released reported %v, want %v", err, ErrMigrationFailed)
		}
		if !errors.Is(err, lock.releaseErr) {
			t.Errorf("the error %v does not carry the reason the mutex stayed held", err)
		}
	})
}

// TestLockTimeoutKeepsItsOwnSentinel pins that a timeout is not reclassified.
func TestLockTimeoutKeepsItsOwnSentinel(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{
			handle:     handle,
			acquireErr: fmt.Errorf("%w: waited 1s for another replica", ErrMigrationLockTimeout),
		}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, lock)
		if !errors.Is(err, ErrMigrationLockTimeout) {
			t.Fatalf("a mutex that timed out reported %v, want %v", err, ErrMigrationLockTimeout)
		}
		if errors.Is(err, ErrMigrationFailed) {
			t.Error("the timeout also matches ErrMigrationFailed, so the host cannot tell a replica " +
				"holding the mutex from a migration that will not apply")
		}
		if lock.releases != 0 {
			t.Errorf("the mutex was released %d times although it was never taken", lock.releases)
		}
		if hasTable(t, handle, recordTableName) || hasTable(t, handle, "widgets") {
			t.Error("the run touched the database although the mutex was never taken")
		}
	})
}

// TestFailingToTakeTheMutexIsErrMigrationFailed pins the other acquire failure.
func TestFailingToTakeTheMutexIsErrMigrationFailed(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{handle: handle, acquireErr: errors.New("no connection to take it on")}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, lock)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a mutex that could not be taken reported %v, want %v", err, ErrMigrationFailed)
		}
		if !errors.Is(err, lock.acquireErr) {
			t.Errorf("the error %v does not carry why the mutex could not be taken", err)
		}
	})
}

func startupWith(t *testing.T, handle *gorm.DB, dialect Dialect, modules ...core.Module) error {
	t.Helper()
	return driveWith(t, handle, dialect, nil, modules...)
}

func driveWith(t *testing.T, handle *gorm.DB, dialect Dialect, included []string, modules ...core.Module) error {
	t.Helper()
	return driveSpec(t, handle, dialect, included, nil, modules...)
}

func driveSpec(t *testing.T, handle *gorm.DB, dialect Dialect, included []string,
	declared []core.Declaration, modules ...core.Module,
) error {
	t.Helper()
	reg := registryOf(modules...)
	reg.Register(core.Module{
		Name: "db.fixture",
		Migrate: func(ctx context.Context, reg *core.Registry, _ any) error {
			return applyMigrations(ctx, reg, handle, dialect, nil)
		},
	})
	served := make(chan struct{})
	reg.Register(core.Module{
		Name:  "startup.probe",
		Serve: func(context.Context, *core.Registry, any) error { close(served); return nil },
	})

	var participants []string
	if included == nil {
		for _, m := range reg.Modules() {
			participants = append(participants, m.Name)
		}
	} else {
		participants = append([]string{"db.fixture", "startup.probe"}, included...)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- reg.Drive(ctx, core.Drive{
			Depth:         core.StageServe,
			Participants:  participants,
			DeclaringOnly: declared,
		})
	}()

	select {
	case err := <-done:
		return err
	case <-served:
	case <-time.After(30 * time.Second):
		t.Fatal("the startup never reached the Serve stage")
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
		return nil
	}
}

func allErrors(err error) []error {
	if err == nil {
		return nil
	}
	out := []error{err}
	if next := errors.Unwrap(err); next != nil {
		return append(out, allErrors(next)...)
	}
	var joined interface{ Unwrap() []error }
	if errors.As(err, &joined) {
		for _, cause := range joined.Unwrap() {
			out = append(out, allErrors(cause)...)
		}
	}
	return out
}

const createWidgetsAndIndex = createWidgets + ";\n" +
	`CREATE INDEX widgets_by_id ON widgets (id);`

// TestEveryStatementInAMigrationFileIsExecuted pins that a migration file may hold more than one
// statement, and that all of them run.
func TestEveryStatementInAMigrationFileIsExecuted(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgetsAndIndex,
		})))

		if err := applyMigrations(t.Context(), reg, handle, dialect, nil); err != nil {
			t.Fatalf("applying a migration of two statements: %v", err)
		}
		if !hasTable(t, handle, "widgets") {
			t.Fatal("the table the first statement creates does not exist, so the file did not run")
		}
		if !handle.Migrator().HasIndex("widgets", "widgets_by_id") {
			t.Error("the index the second statement creates does not exist: only the first statement " +
				"of the file was executed, and the rest was dropped without a word")
		}
		if rows := recordedMigrations(t, handle); len(rows) != 1 {
			t.Errorf("the record table holds %v, want the one migration this file is", rows)
		}
	})
}

// TestTheDialectRunsAMigrationFileWithSeveralStatements is the admission condition under the case
// above, and it belongs to the engine and its driver rather than to this module.
func TestTheDialectRunsAMigrationFileWithSeveralStatements(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		if err := handle.Exec(createWidgetsAndIndex).Error; err != nil {
			t.Fatalf("%s would not run a text of two statements, so a migration set holding one stops "+
				"working on this engine: %v", dialect, err)
		}
		if !hasTable(t, handle, "widgets") {
			t.Error("the first statement of the text did not take effect")
		}
		if !handle.Migrator().HasIndex("widgets", "widgets_by_id") {
			t.Errorf("%s executed only the first statement of the text", dialect)
		}
	})
}

// TestReleaseFailureSaysTheMigrationsWereApplied pins what the run reports when every migration
// went in and only the release failed.
func TestReleaseFailureSaysTheMigrationsWereApplied(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{handle: handle, releaseErr: errors.New("the connection went away")}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, lock)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a mutex that would not be released reported %v, want %v", err, ErrMigrationFailed)
		}
		if !errors.Is(err, lock.releaseErr) {
			t.Errorf("the error %v does not carry the reason the mutex stayed held", err)
		}
		if !strings.Contains(err.Error(), "every migration was applied") {
			t.Errorf("the text %q does not say the migrations went in, so it reads as a migration that "+
				"would not apply and sends the reader to the wrong files", err)
		}
		if !hasTable(t, handle, "widgets") {
			t.Error("the message says every migration was applied, and the table is not there")
		}
		if rows := recordedMigrations(t, handle); len(rows) != 1 {
			t.Errorf("the message says every migration was recorded, and the record table holds %v", rows)
		}
	})
}

// TestReleaseFailureAfterAFailedRunDoesNotClaimSuccess is the other direction.
func TestReleaseFailureAfterAFailedRunDoesNotClaimSuccess(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		lock := &probeLock{handle: handle, releaseErr: errors.New("the connection went away")}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_broken.sql": `THIS IS NOT SQL IN ANY DIALECT`,
		})))

		err := applyMigrations(t.Context(), reg, handle, dialect, lock)
		if !errors.Is(err, ErrMigrationFailed) {
			t.Fatalf("a failed run whose mutex would not be released reported %v, want %v",
				err, ErrMigrationFailed)
		}
		if strings.Contains(err.Error(), "every migration was applied") {
			t.Errorf("the text claims the migrations went in although the run failed: %q", err)
		}
		if !errors.Is(err, lock.releaseErr) {
			t.Errorf("the error %v does not carry the reason the mutex stayed held", err)
		}
		if !strings.Contains(err.Error(), "0001_broken.sql") {
			t.Errorf("the error %v does not name the migration that failed, which is the other reason "+
				"this startup is stopping", err)
		}
	})
}

// TestAPoolOfOneConnectionCarriesTheWholeRun pins that a run needs one connection and not two.
func TestAPoolOfOneConnectionCarriesTheWholeRun(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		pool, err := handle.DB()
		if err != nil {
			t.Fatalf("reaching the fixture's connection pool: %v", err)
		}
		pool.SetMaxOpenConns(1)

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()

		lock := &probeLock{handle: handle}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
			"0002_fill_widgets.sql":   `INSERT INTO widgets (id) VALUES (1)`,
		})))

		if err := applyMigrations(ctx, reg, handle, dialect, lock); err != nil {
			t.Fatalf("a run on a pool of one connection reported %v: the mutex and the migrations have "+
				"to share the one connection the pool allows", err)
		}
		if lock.acquires != 1 || lock.releases != 1 {
			t.Errorf("the mutex was taken %d times and released %d, want once each", lock.acquires, lock.releases)
		}
		if rows := recordedMigrations(t, handle); len(rows) != 2 {
			t.Errorf("the record table holds %v after a run on a pool of one, want both migrations", rows)
		}
	})
}

const pinnedSchema = "pinned_to_the_mutex"

type schemaProbeLock struct {
	schemaAtRelease string
}

func (l *schemaProbeLock) Acquire(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `CREATE SCHEMA `+pinnedSchema); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, `SET search_path TO `+pinnedSchema)
	return err
}

func (l *schemaProbeLock) Release(ctx context.Context, conn *sql.Conn) error {
	return conn.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&l.schemaAtRelease)
}

var _ MigrationLock = (*schemaProbeLock)(nil)

// TestTheWholeRunStaysOnOnePhysicalConnection is the structural half of the case above: not that
// the run fits in one connection, but that all four of its parts are on the same one.
func TestTheWholeRunStaysOnOnePhysicalConnection(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialect Dialect, handle *gorm.DB) {
		if dialect != Postgres {
			t.Skipf("%s has no session-scoped schema to observe the connection through", dialect)
		}
		lock := &schemaProbeLock{}
		reg := settledRegistry(t, declaring("catalog", migrationSet(dialect, map[string]string{
			"0001_create_widgets.sql": createWidgets,
		})))

		if err := applyMigrations(t.Context(), reg, handle, dialect, lock); err != nil {
			t.Fatalf("applying under the mutex: %v", err)
		}
		if lock.schemaAtRelease != pinnedSchema {
			t.Errorf("the mutex was given up on a session whose schema is %q, want %q: the release ran "+
				"on a different connection from the one that took it", lock.schemaAtRelease, pinnedSchema)
		}
		for _, table := range []string{recordTableName, "widgets"} {
			if !tableExistsIn(t, handle, pinnedSchema, table) {
				t.Errorf("%s.%s does not exist, so that statement went to a connection other than the "+
					"one the mutex was taken on", pinnedSchema, table)
			}
			if tableExistsIn(t, handle, "public", table) {
				t.Errorf("public.%s exists, so that statement went to a connection other than the one "+
					"the mutex was taken on", table)
			}
		}
	})
}

// tableExistsIn reports whether a table exists in a named schema. The question
// is asked with both names spelled out, so the answer does not depend on the
// search path of whichever connection the pool hands over to ask it.
func tableExistsIn(t *testing.T, handle *gorm.DB, schema, table string) bool {
	t.Helper()
	var found int64
	err := handle.Raw(`SELECT count(*) FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = ? AND c.relname = ?`, schema, table).Scan(&found).Error
	if err != nil {
		t.Fatalf("looking for %s.%s: %v", schema, table, err)
	}
	return found > 0
}
