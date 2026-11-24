package journal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// An Open connection onto a fileless in-memory database is never taken for the
// delivered one, even with its own audit table.
func TestAnOpenedDatabaseWithNoFileIsNotTakenForTheDeliveredOne(t *testing.T) {
	assembled(t, nil, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database capability: %v", err)
			return errCaseFailed
		}
		opened, err := database.Open(ctx, ":memory:")
		if err != nil {
			t.Errorf("building a connection to an in-memory database: %v", err)
			return errCaseFailed
		}
		defer func() { _ = db.Close(opened) }()
		if err := opened.Exec(createOrders).Error; err != nil {
			t.Errorf("creating the orders table on the in-memory database: %v", err)
			return errCaseFailed
		}
		applyMigrations(t, opened, auditDialect)

		if err := opened.WithContext(ctx).Create(&order{ID: "o1", Note: "in memory"}).Error; err != nil {
			t.Errorf("a model write on the in-memory connection gave %v, want it written and recorded", err)
			return errCaseFailed
		}
		if got := statementRows(t, opened); len(got) != 0 {
			t.Errorf("the in-memory database's own audit table took %d row(s), want none", len(got))
		}
		if got := statementRows(t, database.DB()); len(got) != 1 {
			t.Errorf("the delivered table holds %d statement row(s), want the one write", len(got))
		}
		return nil
	})
}

// Two PostgreSQL connections are taken for one audit table only when the
// server run, the database and the relation all agree.
func TestTwoPostgresConnectionsReachOneTableOnlyOnOneServerRun(t *testing.T) {
	started := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	delivered := reaching{started: started, database: "app", relation: 16384}
	for _, c := range []struct {
		name string
		own  reaching
		same bool
	}{
		{"the delivered table itself", delivered, true},
		{
			"a copy on a server started at another time",
			reaching{started: started.Add(time.Hour), database: "app", relation: 16384},
			false,
		},
		{"another database", reaching{started: started, database: "other", relation: 16384}, false},
		{"another table", reaching{started: started, database: "app", relation: 16385}, false},
		{"no audit table", reaching{started: started, database: "app", absent: true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			own, other := answering(t, c.own), answering(t, delivered)
			same, err := samePostgresTable(t.Context(), own, other)
			if err != nil {
				t.Fatalf("comparing the two connections: %v", err)
			}
			if same != c.same {
				t.Errorf("the two connections were taken for one table: %v, want %v", same, c.same)
			}
		})
	}
}

type reaching struct {
	started  time.Time
	database string
	relation int64
	absent   bool
}

func answering(t *testing.T, r reaching) *sql.DB {
	t.Helper()
	pool := sql.OpenDB(answer{r})
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

type answer struct{ reaching }

func (a answer) Connect(context.Context) (driver.Conn, error) { return a, nil }
func (a answer) Driver() driver.Driver                        { return a }
func (a answer) Open(string) (driver.Conn, error)             { return a, nil }
func (answer) Prepare(string) (driver.Stmt, error)            { return nil, errors.New("not prepared") }
func (answer) Close() error                                   { return nil }
func (answer) Begin() (driver.Tx, error)                      { return nil, errors.New("no transactions") }

func (a answer) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	var relation driver.Value = a.relation
	if a.absent {
		relation = nil
	}
	return &oneRow{values: []driver.Value{a.started, a.database, relation}}, nil
}

type oneRow struct {
	values []driver.Value
	read   bool
}

func (*oneRow) Columns() []string { return []string{"started", "database", "relation"} }
func (*oneRow) Close() error      { return nil }

func (r *oneRow) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(dest, r.values)
	return nil
}
