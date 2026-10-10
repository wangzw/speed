package journal

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/catalog"
)

// assembled drives a real registry to Start and runs action there: the SQLite
// engine, the log module and this module, each through its own descriptor,
// beside whatever extra a case takes part, with the configuration loaded the way
// a host loads it.
//
// These are the cases a handle built by hand cannot stand in for. The database
// module builds the handles, installs this module's plugin on them and applies
// its migrations; what these cases hold is what that assembly does with a
// statement, and a handle built here would only show what the case built.
//
// The action reports through t.Errorf and returns an error to end the drive,
// rather than calling t.Fatalf: the drive still shuts down, so the pools it
// opened are released either way.
func assembled(t *testing.T, extra []core.Module, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	assembledAt(t, filepath.Join(t.TempDir(), "delivered.db"), extra, action)
}

// assembledAt is assembled on the delivered SQLite database the locator names.
func assembledAt(t *testing.T, dsn string, extra []core.Module,
	action func(ctx context.Context, reg *core.Registry) error,
) {
	t.Helper()
	assembledOn(t, sqlite.Module(), "sqlite", dsn, extra, action)
}

// assembledOn is assembled on the engine the module delivers, over the database
// the locator names; engine is the key the module's input items sit under in
// the db namespace.
func assembledOn(t *testing.T, database core.Module, engine, dsn string, extra []core.Module,
	action func(ctx context.Context, reg *core.Registry) error,
) {
	t.Helper()
	run := assembly{
		database: database,
		engine:   engine,
		dsn:      dsn,
		extra:    extra,
		dir:      t.TempDir(),
		depth:    core.StageStart,
	}
	if err := run.drive(t, action); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

// assembly is one run of the module over a database: the engine and the
// database it sits on, the modules that take part beside the three every case
// has, the configuration sections beside the db and log ones, and the stage the
// run advances to.
type assembly struct {
	database core.Module
	engine   string
	dsn      string
	extra    []core.Module
	// sections are the further top-level sections of the configuration, each
	// under the first segment of its namespace: "http", or "audit" for the
	// namespace this module is mounted under.
	sections map[string]any
	// dir is where the configuration and the log file are written.
	dir   string
	depth core.Stage
}

// logFile is where the log module of the run writes. The chain writes to a file
// of the case's own, so the records the modules write at startup stay out of the
// test's output, and the case can read them from there.
func (a assembly) logFile() string { return filepath.Join(a.dir, "log.json") }

// drive loads the configuration the way a host loads it, drives a registry to
// the run's depth, runs action there and hands back what the drive ended with.
//
// A configuration that cannot be loaded fails the case, because that is the
// case's own mistake; what the drive itself fails with is the caller's to read.
func (a assembly) drive(t *testing.T, action func(ctx context.Context, reg *core.Registry) error) error {
	t.Helper()
	document := map[string]any{
		"db": map[string]any{a.engine: map[string]any{"dsn": a.dsn}},
		"log": map[string]any{"outputs": []any{
			map[string]any{"to": "file", "format": "json", "path": a.logFile()},
		}},
	}
	maps.Copy(document, a.sections)
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(a.dir, "config.json")
	if writeErr := os.WriteFile(locator, body, 0o600); writeErr != nil {
		t.Fatalf("writing the primary config source: %v", writeErr)
	}
	modules := append([]core.Module{a.database, log.Module(), Module()}, a.extra...)

	// The loader reads its own registry. The command line is given as empty
	// and the prefix is one no machine carries variables under, so nothing
	// outside the case reaches the values.
	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDAUDITTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	// The reader is the instance of the config module, which has no New. A module
	// with a supplied instance takes part without being named, so the list above
	// leaves it out.
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	return reg.Drive(t.Context(), core.Drive{
		Depth:        a.depth,
		Participants: names,
		Action:       action,
	})
}

// errCaseFailed ends a drive whose case has already reported what went wrong.
var errCaseFailed = errors.New("the case failed")

// openElsewhere builds a second connection through the delivered capability, to
// a database of its own with an orders table and nothing else: no migration is
// applied to it, so it has no audit table.
func openElsewhere(ctx context.Context, t *testing.T, database db.Database) (*gorm.DB, error) {
	t.Helper()
	opened, err := database.Open(ctx, filepath.Join(t.TempDir(), "elsewhere.db"))
	if err != nil {
		t.Errorf("building a second connection: %v", err)
		return nil, errCaseFailed
	}
	if err := opened.Exec(createOrders).Error; err != nil {
		t.Errorf("creating the orders table on the second database: %v", err)
		return nil, errors.Join(errCaseFailed, db.Close(opened))
	}
	return opened, nil
}

// statementRows reads the statement rows a handle's audit table holds.
func statementRows(t *testing.T, handle *gorm.DB) []row {
	t.Helper()
	var rows []row
	if err := handle.Where("source = ?", string(audit.SourceStatement)).Order("id").Find(&rows).Error; err != nil {
		t.Errorf("reading the audit table: %v", err)
	}
	return rows
}

// A model write on a connection built with Open is recorded in the delivered
// database's audit table, and the write succeeds although its own database has
// no audit table at all.
//
// One process keeps one audit table. A row written on the statement's own
// session would need that table on the second database, which carries no
// migration, so every model write there would fail on account of its record.
func TestAWriteOnAnOpenedConnectionIsRecordedInTheDeliveredTable(t *testing.T) {
	assembled(t, nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		opened, err := openElsewhere(ctx, t, database)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close(opened) }()

		if err := opened.WithContext(ctx).Create(&order{ID: "o1", Note: "elsewhere"}).Error; err != nil {
			t.Errorf("a model write on a connection built with Open gave %v, want it written and "+
				"recorded in the delivered table", err)
			return errCaseFailed
		}
		rows := statementRows(t, database.DB())
		if len(rows) != 1 || rows[0].Action != "create" || rows[0].Object != "orders" {
			t.Errorf("the delivered table holds the statement rows %+v, want the one write on the "+
				"second connection", rows)
		}
		return nil
	})
}

// A statement on the delivered connection keeps riding the caller's
// transaction in the assembly: rolled back, it leaves no row.
//
// This is the half the connections built with Open must not take from the
// delivered one. A capture that sent every row through the delivered handle —
// because it took the delivered connection for an opened one — would write the
// row on a connection of its own, leave it behind after the rollback, and on
// this engine wait for the lock the caller's transaction holds.
func TestAWriteOnTheDeliveredConnectionRidesTheCallersTransaction(t *testing.T) {
	assembled(t, nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		handle := database.DB()
		if err := handle.Exec(createOrders).Error; err != nil {
			t.Errorf("creating the orders table: %v", err)
			return errCaseFailed
		}
		changed := errors.New("the caller changed its mind")
		if err := handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&order{ID: "o1", Note: "rolled back"}).Error; err != nil {
				return err
			}
			return changed
		}); !errors.Is(err, changed) {
			t.Errorf("the rolled-back transaction gave %v, want the caller's own error", err)
			return errCaseFailed
		}
		if got := statementRows(t, handle); len(got) != 0 {
			t.Errorf("a rolled-back write on the delivered connection left %d statement row(s)", len(got))
		}
		return nil
	})
}

// A second database that has an audit table of its own still does not receive
// the row: the row belongs to the delivered database whatever the second one
// carries. Two tables an entry could land in is the split the module refuses,
// and whether a second database happens to carry the table is not something a
// host decides by writing the record.
func TestAnOpenedConnectionsOwnAuditTableIsLeftAlone(t *testing.T) {
	assembled(t, nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		opened, err := openElsewhere(ctx, t, database)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close(opened) }()
		applyMigrations(t, opened, auditDialect)

		if err := opened.WithContext(ctx).Create(&order{ID: "o1", Note: "elsewhere"}).Error; err != nil {
			t.Errorf("a model write on a connection built with Open gave %v", err)
			return errCaseFailed
		}
		if got := statementRows(t, opened); len(got) != 0 {
			t.Errorf("the second database's own audit table took %d row(s), want none: the row "+
				"belongs to the delivered one", len(got))
		}
		if got := statementRows(t, database.DB()); len(got) != 1 {
			t.Errorf("the delivered table holds %d statement row(s), want the one write", len(got))
		}
		return nil
	})
}

// The row is written before the second connection's statement commits, in a
// unit of work of its own: a transaction the caller commits on the second
// connection is recorded, and one it rolls back leaves its row behind, because
// the two databases share no transaction.
//
// The rolled-back half is what tells "before the commit" from "after it": a row
// written once the statement had committed would never be written for one that
// was rolled back.
func TestAnOpenedConnectionsTransactionIsRecordedBeforeItCommits(t *testing.T) {
	assembled(t, nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		opened, err := openElsewhere(ctx, t, database)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close(opened) }()

		if err := opened.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Create(&order{ID: "o1", Note: "committed"}).Error
		}); err != nil {
			t.Errorf("committing a write on the second connection: %v", err)
			return errCaseFailed
		}
		changed := errors.New("the caller changed its mind")
		if err := opened.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&order{ID: "o2", Note: "rolled back"}).Error; err != nil {
				return err
			}
			return changed
		}); !errors.Is(err, changed) {
			t.Errorf("the rolled-back transaction gave %v, want the caller's own error", err)
			return errCaseFailed
		}

		if got := statementRows(t, database.DB()); len(got) != 2 {
			t.Errorf("the delivered table holds %d statement row(s), want one per write, the "+
				"rolled-back one included", len(got))
		}
		var committed int64
		if err := opened.Model(&order{}).Count(&committed).Error; err != nil || committed != 1 {
			t.Errorf("the second database holds %d order(s) (%v), want the committed one", committed, err)
		}
		return nil
	})
}

// A delivered audit table that cannot take the row fails the statement on the
// second connection, and its write does not commit: the direction is the one a
// statement on the delivered connection has.
func TestAnOpenedConnectionsWriteFailsWithItsRecord(t *testing.T) {
	assembled(t, nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		opened, err := openElsewhere(ctx, t, database)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close(opened) }()
		// Raw SQL, which the plugin does not capture.
		if err := database.DB().Exec("DROP TABLE " + tableName).Error; err != nil {
			t.Errorf("dropping the delivered audit table: %v", err)
			return errCaseFailed
		}

		// The delivered handle's logger writes a statement that fails as a record,
		// and this one fails on purpose, so the session keeps that record out of the
		// run's output.
		quietly := opened.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
		if err := quietly.Create(&order{ID: "o1", Note: "no trail"}).Error; !errors.Is(err, audit.ErrWriteFailed) {
			t.Errorf("the write gave %v, want ErrWriteFailed", err)
		}
		var committed int64
		if err := opened.Model(&order{}).Count(&committed).Error; err != nil || committed != 0 {
			t.Errorf("the caller was told the write failed, but %d order(s) committed (%v)", committed, err)
		}
		return nil
	})
}

// A connection built with Open onto the delivered database itself is recorded
// the way the delivered connection is: in the delivered table, on the
// statement's own session, inside the statement's own transaction — whichever
// spelling of the database's file the connection was opened with.
//
// Sent through the delivered handle instead, the row would wait for the write
// lock its own statement holds, and every such write would fail. The rolled-back
// half is what tells the statement's own session from the delivered handle: a
// row written through the delivered handle stays behind a rollback.
func TestAConnectionOpenedOntoTheDeliveredDatabaseRecordsOnItsOwnSession(t *testing.T) {
	for _, spelling := range []struct {
		name string
		of   func(t *testing.T, file string) string
	}{
		{name: "the delivered locator", of: func(_ *testing.T, file string) string { return file }},
		{name: "a symbolic link to the file", of: func(t *testing.T, file string) string {
			link := filepath.Join(t.TempDir(), "symbolic.db")
			if err := os.Symlink(file, link); err != nil {
				t.Fatalf("linking to the delivered file: %v", err)
			}
			return link
		}},
		{name: "a hard link to the file", of: func(t *testing.T, file string) string {
			link := filepath.Join(t.TempDir(), "hard.db")
			if err := os.Link(file, link); err != nil {
				t.Fatalf("linking to the delivered file: %v", err)
			}
			return link
		}},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "delivered.db")
			assembledAt(t, withBusyTimeout(file), nil, func(ctx context.Context, reg *core.Registry) error {
				database, err := core.Resolve[db.Database](reg)
				if err != nil {
					t.Errorf("resolving the database capability: %v", err)
					return errCaseFailed
				}
				handle := database.DB()
				if createErr := handle.Exec(createOrders).Error; createErr != nil {
					t.Errorf("creating the orders table: %v", createErr)
					return errCaseFailed
				}
				again, err := database.Open(ctx, spelling.of(t, file))
				if err != nil {
					t.Errorf("opening the delivered database a second time: %v", err)
					return errCaseFailed
				}
				defer func() { _ = db.Close(again) }()

				returned, err := within(selfWaitTimer, func() error {
					return quietly(again).WithContext(ctx).Create(&order{ID: "o1", Note: "committed"}).Error
				})
				if !returned {
					t.Errorf("a write on a connection opened onto the delivered database was still waiting "+
						"after %v", selfWaitTimer)
					return errCaseFailed
				}
				if err != nil {
					t.Errorf("a write on a connection opened onto the delivered database gave %v, want it "+
						"written and recorded", err)
					return errCaseFailed
				}
				changed := errors.New("the caller changed its mind")
				if err := again.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					if err := tx.Create(&order{ID: "o2", Note: "rolled back"}).Error; err != nil {
						return err
					}
					return changed
				}); !errors.Is(err, changed) {
					t.Errorf("the rolled-back transaction gave %v, want the caller's own error", err)
					return errCaseFailed
				}

				if rows := statementRows(t, handle); len(rows) != 1 || rows[0].Object != "orders" {
					t.Errorf("the delivered table holds the statement rows %+v, want the committed write's "+
						"alone: the rolled-back one goes with its statement's transaction", rows)
				}
				var committed int64
				if err := handle.Model(&order{}).Count(&committed).Error; err != nil || committed != 1 {
					t.Errorf("the delivered database holds %d order(s) (%v), want the committed one", committed, err)
				}
				requireBusyTimeoutsAsOpened(ctx, t, handle)
				return nil
			})
		})
	}
}

// trailEntry is a host's read-only model over the audit table for one tenant's
// trail: it embeds the tenant convention, so the tenant filter scopes every read
// through it, and it reads the columns the table's contract promises.
type trailEntry struct {
	tenant.Model
	ID     uint64 `gorm:"column:id"`
	Source string `gorm:"column:source"`
	Action string `gorm:"column:action"`
}

func (trailEntry) TableName() string { return tableName }

// anyTenantsEntry reads the audit table across tenants, with a tenant field that
// takes a NULL as the absence it is.
type anyTenantsEntry struct {
	ID       uint64  `gorm:"column:id"`
	TenantID *string `gorm:"column:tenant_id"`
	Action   string  `gorm:"column:action"`
}

func (anyTenantsEntry) TableName() string { return tableName }

// A host reads one tenant's trail through the tenant filter, with a read-only
// model that embeds the convention: it reads that tenant's rows and no row
// without a tenant, a disabled tenant's trail is read after entering it with
// EnterRegistered, and a read across tenants through a type whose tenant field
// is nullable tells the rows with no tenant from the rest.
//
// This is the read the table's contract is for: the tenant column is tenant_id,
// holding the identifier's text or NULL, and the id key is unique over the
// table. The same read through the embedding type across tenants gives the rows
// without a tenant an empty identifier and no error, which is why a read across
// tenants takes a type of its own.
func TestATenantsTrailIsReadThroughTheTenantFilter(t *testing.T) {
	assembled(t, []core.Module{catalog.Module()}, func(ctx context.Context, reg *core.Registry) error {
		roster, err := core.Resolve[tenant.Catalog](reg)
		if err != nil {
			t.Errorf("resolving the tenant roster: %v", err)
			return errCaseFailed
		}
		recorder, err := core.Resolve[audit.Audit](reg)
		if err != nil {
			t.Errorf("resolving the audit capability: %v", err)
			return errCaseFailed
		}
		for _, registered := range []tenant.Tenant{
			{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive},
			{ID: "t_globex", Alias: "globex", Status: tenant.StatusActive},
		} {
			if putErr := roster.Put(ctx, registered); putErr != nil {
				t.Errorf("registering %s: %v", registered.ID, putErr)
				return errCaseFailed
			}
		}
		for _, entry := range []struct {
			tenant tenant.ID
			action string
		}{{"t_acme", "acme-export"}, {"t_globex", "globex-export"}, {"", "system-sweep"}} {
			at := ctx
			if entry.tenant != "" {
				if at, err = roster.Enter(ctx, entry.tenant); err != nil {
					t.Errorf("entering %s: %v", entry.tenant, err)
					return errCaseFailed
				}
			}
			if recordErr := recorder.Record(at, audit.Entry{Action: entry.action}); recordErr != nil {
				t.Errorf("recording %s: %v", entry.action, recordErr)
				return errCaseFailed
			}
		}
		if statusErr := roster.SetStatus(ctx, "t_globex", tenant.StatusDisabled); statusErr != nil {
			t.Errorf("disabling t_globex: %v", statusErr)
			return errCaseFailed
		}
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		handle := database.DB()

		trailOf := func(at context.Context) []string {
			var entries []trailEntry
			if readErr := handle.WithContext(at).Where("source = ?", string(audit.SourceSeam)).
				Order("id").Find(&entries).Error; readErr != nil {
				t.Errorf("reading a tenant's trail: %v", readErr)
			}
			actions := make([]string, 0, len(entries))
			for _, e := range entries {
				actions = append(actions, string(e.TenantID)+":"+e.Action)
			}
			return actions
		}
		acme, err := roster.Enter(ctx, "t_acme")
		if err != nil {
			t.Errorf("entering t_acme: %v", err)
			return errCaseFailed
		}
		if got := trailOf(acme); len(got) != 1 || got[0] != "t_acme:acme-export" {
			t.Errorf("t_acme's trail reads %v, want its own entry alone", got)
		}
		globex, err := roster.EnterRegistered(ctx, "t_globex")
		if err != nil {
			t.Errorf("entering the disabled t_globex for its trail: %v", err)
			return errCaseFailed
		}
		if got := trailOf(globex); len(got) != 1 || got[0] != "t_globex:globex-export" {
			t.Errorf("the disabled t_globex's trail reads %v, want its own entry alone", got)
		}

		crossing := tenant.CrossTenant(ctx)
		var every []anyTenantsEntry
		if err := handle.WithContext(crossing).Where("source = ?", string(audit.SourceSeam)).
			Order("id").Find(&every).Error; err != nil {
			t.Errorf("reading the trail across tenants: %v", err)
			return errCaseFailed
		}
		if len(every) != 3 || every[2].Action != "system-sweep" || every[2].TenantID != nil {
			read := make([]string, 0, len(every))
			for _, e := range every {
				read = append(read, text(e.TenantID)+":"+e.Action)
			}
			t.Errorf("the trail across tenants reads %v, want three entries, the last with a NULL tenant", read)
		}
		var embedded []trailEntry
		if err := handle.WithContext(crossing).Where("source = ?", string(audit.SourceSeam)).
			Order("id").Find(&embedded).Error; err != nil {
			t.Errorf("reading the trail across tenants through the embedding type gave %v, want the rows", err)
			return errCaseFailed
		}
		if len(embedded) != 3 || embedded[2].TenantID != "" {
			t.Errorf("the trail across tenants through the embedding type reads %+v, want the entry with no "+
				"tenant read with an empty identifier", embedded)
		}
		return nil
	})
}

// A statement issued across tenants from a context that belonged to one has no
// tenant in its row, and neither has an entry handed to the seam there.
//
// The context is a tenant's own, entered through the roster the way a path with
// no request behind it enters one, so the case reaches the context shape a
// request handler holds. A reader of the marked context that still found the
// tenant it was derived from would file an unscoped statement under that
// tenant's scope — and the control half of the case shows the tenant is filled
// where it belongs.
func TestAWriteAcrossTenantsFromATenantsContextHasNoTenant(t *testing.T) {
	assembled(t, []core.Module{catalog.Module()}, func(ctx context.Context, reg *core.Registry) error {
		roster, err := core.Resolve[tenant.Catalog](reg)
		if err != nil {
			t.Errorf("resolving the tenant roster: %v", err)
			return errCaseFailed
		}
		registered := tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
		if putErr := roster.Put(ctx, registered); putErr != nil {
			t.Errorf("registering the tenant: %v", putErr)
			return errCaseFailed
		}
		entered, err := roster.Enter(ctx, "t_acme")
		if err != nil {
			t.Errorf("entering the tenant: %v", err)
			return errCaseFailed
		}
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		recorder, err := core.Resolve[audit.Audit](reg)
		if err != nil {
			t.Errorf("resolving the audit capability: %v", err)
			return errCaseFailed
		}
		handle := database.DB()
		if err := handle.Exec(createOrders).Error; err != nil {
			t.Errorf("creating the orders table: %v", err)
			return errCaseFailed
		}

		if err := handle.WithContext(entered).Create(&order{ID: "o1", Note: "own"}).Error; err != nil {
			t.Errorf("writing under the tenant's context: %v", err)
			return errCaseFailed
		}
		crossing := tenant.CrossTenant(entered)
		if err := handle.WithContext(crossing).Create(&order{ID: "o2", Note: "across"}).Error; err != nil {
			t.Errorf("writing across tenants: %v", err)
			return errCaseFailed
		}
		if err := recorder.Record(crossing, audit.Entry{Action: "export", Purpose: "support"}); err != nil {
			t.Errorf("recording across tenants: %v", err)
			return errCaseFailed
		}

		var rows []row
		if err := handle.Where("object = ? OR source = ?", "orders", string(audit.SourceSeam)).
			Order("id").Find(&rows).Error; err != nil {
			t.Errorf("reading the audit table: %v", err)
			return errCaseFailed
		}
		if len(rows) != 3 {
			t.Errorf("want the two writes and the seam entry recorded, got %+v", rows)
			return errCaseFailed
		}
		if own := rows[0]; own.TenantID == nil || *own.TenantID != "t_acme" {
			t.Errorf("the write under the tenant's context has tenant %v, want t_acme", own.TenantID)
		}
		for _, crossed := range rows[1:] {
			if crossed.TenantID != nil {
				t.Errorf("the %s row made across tenants has tenant %q, want none", crossed.Source, *crossed.TenantID)
			}
		}
		return nil
	})
}
