package db

import (
	"cmp"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
)

// MigrationLock is the cross-process mutex the migration run is held under. An
// implementation subpackage supplies one when its dialect appears in
// multi-replica deployments.
//
// Both halves take the connection the whole run is pinned to: a mutex of this
// kind belongs to the session that took it, and taken on one connection and
// given up on another it is never given up at all.
//
// Acquire waits within the implementation's own deadline and reports
// ErrMigrationLockTimeout once it passes. The mutex is taken before the record
// table is created and released after the last migration has been applied.
type MigrationLock interface {
	// Acquire takes the mutex on conn, waiting for another process to release
	// it.
	Acquire(ctx context.Context, conn *sql.Conn) error
	// Release gives the mutex up on conn, the connection it was taken on. It
	// runs on the failing path too.
	Release(ctx context.Context, conn *sql.Conn) error
}

// recordTableName is the table that records which migrations have been applied,
// a row per applied migration keyed by the declaring module and the file name.
const recordTableName = "db_migrations"

const (
	createRecordTable = `CREATE TABLE IF NOT EXISTS db_migrations (
	module TEXT NOT NULL,
	file TEXT NOT NULL,
	applied_at TIMESTAMP NOT NULL,
	PRIMARY KEY (module, file)
)`
	selectRecords = `SELECT module, file FROM db_migrations`
	insertRecord  = `INSERT INTO db_migrations (module, file, applied_at) VALUES (?, ?, ?)`
)

type migration struct {
	module string
	file   string
	body   string
}

type recordKey struct {
	module string
	file   string
}

// applyMigrations applies every declared migration the record table does not
// list, recording each one. One connection is borrowed and kept for the whole
// run, so the session-scoped mutex and the statements it orders sit on the same
// connection and the run occupies one connection rather than two.
func applyMigrations(ctx context.Context, reg *core.Registry, handle *gorm.DB, dialect Dialect, lock MigrationLock) (err error) {
	pool, err := handle.DB()
	if err != nil {
		return fmt.Errorf("%w: the %s handle has no connection pool to apply migrations on: %w",
			ErrMigrationFailed, dialect, err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: taking the connection to apply the %s migrations on failed: %w",
			ErrMigrationFailed, dialect, err)
	}
	// Set when the mutex would not be given up: the connection is then thrown
	// away rather than handed back. See discard.
	stranded := false
	defer func() {
		if stranded {
			err = errors.Join(err, discard(conn))
			return
		}
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf(
				"%w: handing the migration run's connection back to the pool failed: %w",
				ErrMigrationFailed, closeErr))
		}
	}()

	if lock != nil {
		if lockErr := lock.Acquire(ctx, conn); lockErr != nil {
			// A timeout travels as it is: wrapping it would make both
			// sentinels match and point the host at the migrations.
			if errors.Is(lockErr, ErrMigrationLockTimeout) {
				return lockErr
			}
			return fmt.Errorf("%w: taking the migration mutex on the %s database failed: %w",
				ErrMigrationFailed, dialect, lockErr)
		}
		defer func() {
			applied := err == nil
			if releaseErr := lock.Release(ctx, conn); releaseErr != nil {
				stranded = true
				err = errors.Join(err, releaseFailed(dialect, applied, releaseErr))
			}
		}()
	}
	return applyUnderLock(reg, pinned(ctx, handle, conn), dialect)
}

// pinned points the session's ConnPool at the borrowed connection, which keeps
// the run and its session-scoped mutex off whichever connection the pool hands
// out next.
func pinned(ctx context.Context, handle *gorm.DB, conn *sql.Conn) *gorm.DB {
	session := handle.WithContext(ctx)
	session.Statement.ConnPool = conn
	return session
}

// releaseFailed says where the failure fell, because ErrMigrationFailed alone
// would point the operator at migration files that are fine.
func releaseFailed(dialect Dialect, applied bool, cause error) error {
	if applied {
		return fmt.Errorf("%w: every migration was applied and recorded, and what failed afterwards is "+
			"giving the migration mutex on the %s database up — there is nothing wrong with the "+
			"migrations. This startup stops so that the session holding the mutex ends and the other "+
			"replicas are not left waiting for it: %w",
			ErrMigrationFailed, dialect, cause)
	}
	return fmt.Errorf("%w: the migration run did not finish, and giving the migration mutex on the %s "+
		"database up afterwards failed as well, so the other replicas wait for it until this session "+
		"ends: %w",
		ErrMigrationFailed, dialect, cause)
}

// discard throws the run's connection away instead of handing it back, because
// ending the session is the one remaining way to release a mutex that could not
// be given up.
func discard(conn *sql.Conn) error {
	// Raw reports the callback's error; ErrBadConn asks database/sql to drop
	// the connection, so seeing it back is the success case, as is ErrConnDone.
	err := conn.Raw(func(any) error { return driver.ErrBadConn })
	if err != nil && !errors.Is(err, driver.ErrBadConn) && !errors.Is(err, sql.ErrConnDone) {
		return fmt.Errorf("%w: dropping the migration run's connection failed, and the mutex stays held "+
			"until the server drops that session: %w", ErrMigrationFailed, err)
	}
	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return fmt.Errorf("%w: closing the migration run's connection failed: %w", ErrMigrationFailed, err)
	}
	return nil
}

// applyUnderLock is the run itself, with the mutex already held. The record
// table is looked up before it is created: PostgreSQL checks the privilege to
// create in the schema before it looks for the table, so creating it on every
// start would demand DDL privilege of a read-only service role.
func applyUnderLock(reg *core.Registry, session *gorm.DB, dialect Dialect) error {
	if !session.Migrator().HasTable(recordTableName) {
		if err := session.Exec(createRecordTable).Error; err != nil {
			return fmt.Errorf("%w: creating the migration record table %q failed: %w",
				ErrMigrationFailed, recordTableName, err)
		}
	}
	applied, err := readRecords(session)
	if err != nil {
		return err
	}
	planned, err := collectMigrations(reg, dialect)
	if err != nil {
		return err
	}
	for _, m := range planned {
		if _, done := applied[recordKey{module: m.module, file: m.file}]; done {
			continue
		}
		if err := applyOne(session, m); err != nil {
			return err
		}
	}
	return nil
}

func readRecords(session *gorm.DB) (map[recordKey]struct{}, error) {
	var rows []struct {
		Module string
		File   string
	}
	if err := session.Raw(selectRecords).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("%w: reading the migration record table %q failed: %w",
			ErrMigrationFailed, recordTableName, err)
	}
	applied := make(map[recordKey]struct{}, len(rows))
	for _, row := range rows {
		applied[recordKey{module: row.Module, file: row.File}] = struct{}{}
	}
	return applied, nil
}

// applyOne executes a migration and records it in one transaction, so it is
// neither recorded without running nor run twice. Holding the two together
// rests on the dialect putting DDL inside a transaction, and the file goes to
// the driver whole: splitting it here would mean parsing SQL.
func applyOne(session *gorm.DB, m migration) error {
	err := session.Transaction(func(tx *gorm.DB) error {
		if execErr := tx.Exec(m.body).Error; execErr != nil {
			return fmt.Errorf("executing it failed: %w", execErr)
		}
		if recordErr := tx.Exec(insertRecord, m.module, m.file, time.Now().UTC()).Error; recordErr != nil {
			return fmt.Errorf("recording it in %q failed: %w", recordTableName, recordErr)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w: migration %q declared by module %q: %w",
			ErrMigrationFailed, m.file, m.module, err)
	}
	return nil
}

// collectMigrations turns the declared resources into the list to apply, in the
// order to apply it.
//
// The declaring set forms the list and the participating set only narrows it;
// the two must not be collapsed into one. Collected by participation alone, a
// command that runs only the engine, whose tables belong to modules the closure
// never reaches, applies no migration at all and exits successfully.
//
// The order comes from dependencyRank over every registered module, so a set of
// migrations is applied in the same sequence whichever command applies them.
func collectMigrations(reg *core.Registry, dialect Dialect) ([]migration, error) {
	rank := dependencyRank(reg)

	byModule := make(map[string][]Migrations)
	var modules []string
	for _, declared := range core.Resources[Migrations](reg) {
		// Both questions: a module the command did not take in keeps no
		// tables, and a participating module that resolution stood down has
		// its tables withheld.
		if !reg.Declaring(declared.Module) {
			continue
		}
		if reg.Participating(declared.Module) {
			if state, known := reg.Enablement(declared.Module); known && state.State == core.StateDisabled {
				continue
			}
		}
		if _, seen := byModule[declared.Module]; !seen {
			modules = append(modules, declared.Module)
		}
		byModule[declared.Module] = append(byModule[declared.Module], declared.Value)
	}
	inDependencyOrder(rank, modules)

	var planned []migration
	for _, module := range modules {
		files, err := filesFor(module, byModule[module], dialect)
		if err != nil {
			return nil, err
		}
		planned = append(planned, files...)
	}
	return planned, nil
}

// filesFor reads one module's migrations for the running dialect, in file-name
// order, which is the whole of the order within a module.
func filesFor(module string, sets []Migrations, dialect Dialect) ([]migration, error) {
	var files []migration
	seen := make(map[string]struct{})
	for _, set := range sets {
		if set.FS == nil {
			continue
		}
		dir, err := fs.Sub(set.FS, string(dialect))
		if err != nil {
			return nil, fmt.Errorf("%w: reaching the %q migrations declared by module %q failed: %w",
				ErrMigrationFailed, dialect, module, err)
		}
		entries, err := fs.ReadDir(dir, ".")
		if err != nil {
			// No subdirectory for this dialect is zero migrations, not a
			// failure.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("%w: reading the %q migrations declared by module %q failed: %w",
				ErrMigrationFailed, dialect, module, err)
		}
		for _, entry := range entries {
			if err := admit(module, dialect, entry); err != nil {
				return nil, err
			}
			if _, duplicate := seen[entry.Name()]; duplicate {
				// The record table is keyed by module and file name, so
				// the second one would count as applied without running.
				return nil, fmt.Errorf("%w: module %q declares %q twice among its %q migrations. "+
					"The record table is keyed by module and file name, so the second one would "+
					"count as applied without ever running",
					ErrMigrationFailed, module, entry.Name(), dialect)
			}
			seen[entry.Name()] = struct{}{}
			body, err := fs.ReadFile(dir, entry.Name())
			if err != nil {
				return nil, fmt.Errorf("%w: reading migration %q declared by module %q failed: %w",
					ErrMigrationFailed, entry.Name(), module, err)
			}
			files = append(files, migration{module: module, file: entry.Name(), body: string(body)})
		}
	}
	slices.SortFunc(files, func(a, b migration) int { return cmp.Compare(a.file, b.file) })
	return files, nil
}

const migrationFileExtension = ".sql"

// admit reports why an entry of a dialect's migration directory cannot be
// applied. The directory holds regular .sql files and nothing else; anything
// else is an error rather than something to pass over.
func admit(module string, dialect Dialect, entry fs.DirEntry) error {
	switch {
	case entry.IsDir():
		return fmt.Errorf("%w: module %q has a directory %q inside its %q migrations, and only %s files "+
			"are applied. Move the migrations up into %q or drop the directory",
			ErrMigrationFailed, module, entry.Name(), dialect, migrationFileExtension, dialect)
	case !entry.Type().IsRegular():
		return fmt.Errorf("%w: %q inside module %q's %q migrations is not a regular file, and only "+
			"regular %s files are applied",
			ErrMigrationFailed, entry.Name(), module, dialect, migrationFileExtension)
	case path.Ext(entry.Name()) != migrationFileExtension:
		return fmt.Errorf("%w: module %q has %q inside its %q migrations, and only %s files are applied. "+
			"Rename it if it is a migration; move it out of the %q directory if it is not",
			ErrMigrationFailed, module, entry.Name(), dialect, migrationFileExtension, dialect)
	}
	return nil
}
