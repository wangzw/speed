//go:build integration

package journal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// pgDialect is the subdirectory of the migrations the module ships for
// PostgreSQL. The cases below build the audit table from those files, so the
// files themselves are what they exercise.
const pgDialect = "postgres"

// openPostgres builds a handle on a database of this test's own on the shared
// PostgreSQL fixture, with the capture plugin installed, the orders table
// created and the module's own PostgreSQL migrations applied.
//
// These are the cases the in-memory handle cannot stand in for: a statement
// that fails inside a PostgreSQL transaction leaves that transaction unable to
// run anything else, and two replicas' writers contend on one table from two
// pools — properties of the engine and not of the plugin.
func openPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	handle, _ := openPostgresReplicas(t)
	return handle
}

// openPostgresReplicas builds two handles on one database of this test's own, as
// two replicas sharing an audit table hold them: a pool each, the capture plugin
// on each, and the tables built once.
func openPostgresReplicas(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dsn := pgtest.Acquire(t)
	first, second := connectPostgres(t, dsn), connectPostgres(t, dsn)
	if err := first.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	applyMigrations(t, first, pgDialect)
	return first, second
}

// connectPostgres is the handle the database module delivers over the database
// the locator names, released when the case ends, with the capture plugin
// installed. Each call is a pool of its own, as a replica's is.
func connectPostgres(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	handle := quietly(dbtest.Postgres(t, dsn, nil).DB())
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	return handle
}

// The PostgreSQL migration builds a table the statement path and the seam both
// write into, with the absent tenant stored as NULL rather than as an empty
// string and the masked key replaced before the row is written.
func TestPostgresMigrationTakesTheStatementAndTheSeam(t *testing.T) {
	handle := openPostgres(t)
	if err := handle.Create(&order{ID: "o1", Note: "first"}).Error; err != nil {
		t.Fatalf("writing the order: %v", err)
	}
	j := newTestJournal(handle, settings{})
	if err := j.Record(context.Background(), audit.Entry{
		Action: "export", Object: "orders", Outcome: "ok",
		Detail: map[string]string{"token": "t", "note": "kept"},
	}); err != nil {
		t.Fatalf("recording through the seam: %v", err)
	}

	rows := auditRows(t, handle)
	if len(rows) != 2 {
		t.Fatalf("want a statement row and a seam row, got %d", len(rows))
	}
	statement, seam := rows[0], rows[1]
	if statement.Source != string(audit.SourceStatement) || statement.Action != "create" ||
		statement.Object != "orders" || statement.Outcome != "ok" ||
		statement.Detail != `{"rows_affected":"1"}` {
		t.Errorf("unexpected statement row: %+v", statement)
	}
	if statement.TenantID != nil {
		t.Errorf("a write with no tenant in its context stored tenant %q, want NULL", *statement.TenantID)
	}
	if seam.Source != string(audit.SourceSeam) || seam.Detail != `{"note":"kept","token":"[REDACTED]"}` {
		t.Errorf("unexpected seam row: %+v", seam)
	}
}

// A statement row rides the statement's transaction on PostgreSQL as well.
func TestPostgresStatementRowRidesTheTransaction(t *testing.T) {
	handle := openPostgres(t)
	changed := errors.New("the caller changed its mind")
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&order{ID: "o1", Note: "rolled back"}).Error; err != nil {
			return err
		}
		return changed
	})
	if !errors.Is(err, changed) {
		t.Fatalf("the transaction gave %v, want the caller's own error", err)
	}
	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("a rolled-back write left %d audit row(s) behind", got)
	}
}

// A duplicate key on PostgreSQL reaches the caller as the engine's own error.
//
// The engine refuses every further statement in a transaction one has failed
// in, so a record attempted for the failed statement cannot be written there at
// all; attempting it only adds a second failure to the caller's error.
func TestPostgresDuplicateKeyKeepsTheEnginesError(t *testing.T) {
	handle := openPostgres(t)
	if err := handle.Create(&order{ID: "o1", Note: "first"}).Error; err != nil {
		t.Fatalf("writing the order: %v", err)
	}

	err := handle.Create(&order{ID: "o1", Note: "again"}).Error
	var engine *pgconn.PgError
	if !errors.As(err, &engine) || engine.Code != "23505" {
		t.Fatalf("the duplicate gave %v, want the engine's unique violation", err)
	}
	if errors.Is(err, audit.ErrWriteFailed) || strings.Contains(err.Error(), audit.ErrWriteFailed.Error()) {
		t.Fatalf("a duplicate key was reported as an audit write failure: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 1 {
		t.Fatalf("want only the successful write recorded, got %d row(s)", got)
	}
}

// Two replicas delivering one event at the same moment leave one row between
// them, and neither reports a failure.
//
// Each replica writes from a pool of its own, so what decides is the table: a
// replica that looked for the row before inserting would find none on both
// sides and write two, and one that inserted into the unique key without
// yielding on it would have every delivery after the first fail and reach the
// failure signal, reporting a healthy table as broken.
func TestPostgresAnEventDeliveredToEveryReplicaIsRecordedOnce(t *testing.T) {
	first, second := openPostgresReplicas(t)
	var reports [2]bytes.Buffer
	buses := [2]*recordingBus{newRecordingBus(), newRecordingBus()}
	for i, handle := range []*gorm.DB{first, second} {
		j := newTestJournal(handle, settings{events: []string{"order.updated"}})
		j.signal = &signal{out: &reports[i]}
		j.subscribe(buses[i])
	}
	evt := event.Event{ID: event.NewID(), Type: "order.updated", Payload: map[string]any{"object": "order/o1"}}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = buses[i%2].Publish(context.Background(), evt)
		}()
	}
	close(start)
	wg.Wait()

	rows := auditRows(t, first)
	if len(rows) != 1 {
		t.Fatalf("one event delivered eight times across two replicas left %d row(s), want 1", len(rows))
	}
	if rows[0].EventID == nil || *rows[0].EventID != evt.ID {
		t.Errorf("the row records event %v, want %q", rows[0].EventID, evt.ID)
	}
	for i := range reports {
		if reports[i].Len() != 0 {
			t.Errorf("replica %d reported a failure for a delivery the table already held: %s",
				i, reports[i].String())
		}
	}
}

// The row an event leaves on PostgreSQL is the first delivery's, when the later
// delivery comes from another replica's pool.
func TestPostgresTheFirstDeliveryOfAnEventIsTheOneKept(t *testing.T) {
	first, second := openPostgresReplicas(t)
	requireTheFirstDeliveryIsKept(t, first, second)
}

// assembledOnPostgres drives the assembly on a PostgreSQL database of this
// case's own and hands the action that database's locator.
func assembledOnPostgres(t *testing.T, action func(ctx context.Context, reg *core.Registry, dsn string) error) {
	t.Helper()
	dsn := pgtest.Acquire(t)
	assembledOn(t, dbpostgres.Module(), "postgres", dsn, nil, func(ctx context.Context, reg *core.Registry) error {
		return action(ctx, reg, dsn)
	})
}

// A connection built with Open onto the delivered PostgreSQL database records
// on the statement's own session, as the delivered connection does: a write it
// rolls back leaves no row behind. The row written through the delivered handle
// would be in a transaction of its own, and would stay.
func TestPostgresAConnectionOpenedOntoTheDeliveredDatabaseRidesItsTransaction(t *testing.T) {
	assembledOnPostgres(t, func(ctx context.Context, reg *core.Registry, dsn string) error {
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
		again, err := database.Open(ctx, dsn)
		if err != nil {
			t.Errorf("opening the delivered database a second time: %v", err)
			return errCaseFailed
		}
		defer func() { _ = db.Close(again) }()

		if err := again.WithContext(ctx).Create(&order{ID: "o1", Note: "committed"}).Error; err != nil {
			t.Errorf("a write on a connection opened onto the delivered database gave %v", err)
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
		if rows := statementRows(t, handle); len(rows) != 1 {
			t.Errorf("the delivered table holds %d statement row(s), want the committed write's alone: the "+
				"rolled-back one goes with its statement's transaction", len(rows))
		}
		return nil
	})
}

// A connection built with Open that reaches an audit table other than the
// delivered one — another database on the delivered server, or another schema
// of the delivered database through its search path — does not take the row,
// which goes to the delivered table.
//
// The second case is what a comparison of the database alone would miss: one
// database, and the table's name resolving to two relations in it.
func TestPostgresAConnectionOpenedOntoAnotherAuditTableRecordsInTheDeliveredOne(t *testing.T) {
	for _, target := range []struct {
		name string
		// build prepares the other table and returns the locator reaching it.
		build func(t *testing.T, handle *gorm.DB, dsn string) string
	}{
		{name: "another database on the delivered server", build: func(t *testing.T, _ *gorm.DB, _ string) string {
			return pgtest.Acquire(t)
		}},
		{name: "another schema of the delivered database", build: func(t *testing.T, handle *gorm.DB, dsn string) string {
			if err := handle.Exec(`CREATE SCHEMA elsewhere`).Error; err != nil {
				t.Fatalf("creating the other schema: %v", err)
			}
			return dsn + "&search_path=elsewhere"
		}},
	} {
		t.Run(target.name, func(t *testing.T) {
			assembledOnPostgres(t, func(ctx context.Context, reg *core.Registry, dsn string) error {
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
				elsewhere := target.build(t, handle, dsn)
				other := connectPostgres(t, elsewhere)
				if createErr := other.Exec(createOrders).Error; createErr != nil {
					t.Errorf("creating the other orders table: %v", createErr)
					return errCaseFailed
				}
				applyMigrations(t, other, pgDialect)
				opened, err := database.Open(ctx, elsewhere)
				if err != nil {
					t.Errorf("building a second connection: %v", err)
					return errCaseFailed
				}
				defer func() { _ = db.Close(opened) }()

				if err := opened.WithContext(ctx).Create(&order{ID: "o1", Note: "elsewhere"}).Error; err != nil {
					t.Errorf("a model write on a connection built with Open gave %v", err)
					return errCaseFailed
				}
				if got := statementRows(t, other); len(got) != 0 {
					t.Errorf("the other audit table took %d row(s), want none: the row belongs to the "+
						"delivered one", len(got))
				}
				if got := statementRows(t, handle); len(got) != 1 {
					t.Errorf("the delivered table holds %d statement row(s), want the one write", len(got))
				}
				return nil
			})
		})
	}
}

// The migration adding the event column keeps a table the first one built.
func TestPostgresTheEventColumnMigrationKeepsAnExistingTable(t *testing.T) {
	handle := connectPostgres(t, pgtest.Acquire(t))
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	requireTheUpgradeKeepsTheTable(t, handle, pgDialect)
}
