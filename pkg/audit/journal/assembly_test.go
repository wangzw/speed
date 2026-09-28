package journal

import (
	"context"
	"errors"
	"fmt"
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
	dir := t.TempDir()
	// The log chain writes to a file of the case's own, so the records the
	// modules write at startup stay out of the test's output.
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}},"log":{"outputs":[{"to":"file","format":"json","path":%q}]}}`,
		filepath.Join(dir, "delivered.db"), filepath.Join(dir, "log.json"))
	locator := filepath.Join(dir, "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	modules := append([]core.Module{sqlite.Module(), log.Module(), Module()}, extra...)

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
	reg.Supply((*config.Reader)(nil), reader)
	if err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	}); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
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

		// GORM prints a failed statement, and this one fails on purpose.
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
