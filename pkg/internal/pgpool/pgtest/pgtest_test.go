package pgtest_test

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the driver this package opens through

	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestEachAcquisitionGetsAnEmptyDatabase holds this package to the isolation
// every case built on it assumes.
//
// The migration cases assert on which tables exist and on what the record table
// holds. make check runs the tests twice, once with the race detector, and go
// test runs packages in parallel, so a shared database would carry one run's
// tables into the next — and the cross-process case needs a database on which
// nothing has been applied at all, or the replica that should apply the
// migration finds the work already done and the case passes without testing
// anything.
func TestEachAcquisitionGetsAnEmptyDatabase(t *testing.T) {
	first, second := pgtest.Acquire(t), pgtest.Acquire(t)
	if first == second {
		t.Fatalf("two acquisitions handed back the same locator %q, so the cases share a database", first)
	}

	firstHandle, secondHandle := open(t, first), open(t, second)
	if _, err := firstHandle.Exec(`CREATE TABLE only_in_the_first (id INTEGER)`); err != nil {
		t.Fatalf("creating a table on the first database: %v", err)
	}
	if tableExists(t, secondHandle, "only_in_the_first") {
		t.Error("a table created on the first database is visible on the second, so the two are one database")
	}
	if !tableExists(t, firstHandle, "only_in_the_first") {
		t.Error("the table is not visible on the database it was created on, so the check above proves nothing")
	}
}

// TestAcquiredDatabaseNamesCarryTheOwningProcess pins the naming this package's
// housekeeping depends on: a run killed part-way leaves its databases on the
// shared container, and the owning process id in the name is how a later run
// tells the leftovers of a dead process from the databases a live one is using
// right now.
func TestAcquiredDatabaseNamesCarryTheOwningProcess(t *testing.T) {
	dsn := pgtest.Acquire(t)
	_, database, found := strings.Cut(strings.TrimSuffix(dsn, "?sslmode=disable"), "127.0.0.1:")
	if !found {
		t.Fatalf("the locator %q does not have the shape this assertion reads", dsn)
	}
	_, name, found := strings.Cut(database, "/")
	if !found {
		t.Fatalf("the locator %q carries no database name", dsn)
	}
	if !strings.HasPrefix(name, "pgtest_p") || !strings.Contains(name, "_n") {
		t.Errorf("the database name %q does not carry the owning process id, so a later run cannot tell "+
			"a dead run's leftovers from a live run's databases", name)
	}
}

// TestADMLRoleWritesRowsAndCreatesNothing holds DMLRole to the credential the
// cases built on it describe: it reads and writes the rows of a table that was
// there when it was made, it creates nothing, and its name carries the owning
// process, which is how the housekeeping tells a dead run's role from a live
// one's.
func TestADMLRoleWritesRowsAndCreatesNothing(t *testing.T) {
	dsn := pgtest.Acquire(t)
	owner := open(t, dsn)
	if _, err := owner.Exec(`CREATE TABLE rows_only (id BIGSERIAL PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatalf("creating the table as the owner: %v", err)
	}
	roleDSN := pgtest.DMLRole(t, dsn)
	parsed, err := url.Parse(roleDSN)
	if err != nil {
		t.Fatalf("reading the role's locator: %v", err)
	}
	if name := parsed.User.Username(); !strings.HasPrefix(name, "pgtest_p") || !strings.Contains(name, "_r") {
		t.Errorf("the role name %q does not carry the owning process id, so a later run cannot tell a "+
			"dead run's role from a live run's", name)
	}

	asRole := open(t, roleDSN)
	if _, err := asRole.Exec(`INSERT INTO rows_only (note) VALUES ('written')`); err != nil {
		t.Errorf("the role cannot write a row of a table that was there when it was made: %v", err)
	}
	if _, err := asRole.Exec(`UPDATE rows_only SET note = 'rewritten'`); err != nil {
		t.Errorf("the role cannot update a row: %v", err)
	}
	if _, err := asRole.Exec(`DELETE FROM rows_only`); err != nil {
		t.Errorf("the role cannot delete a row: %v", err)
	}
	if _, err := asRole.Exec(`CREATE TABLE created_by_the_role (id INTEGER)`); err == nil {
		t.Error("the role created a table")
	}
}

// open opens a handle on a locator this package handed back.
func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	handle, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening %q: %v", dsn, err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("closing the handle on %q: %v", dsn, err)
		}
	})
	return handle
}

// tableExists reports whether a table is in the handle's database.
func tableExists(t *testing.T, handle *sql.DB, table string) bool {
	t.Helper()
	var name sql.NullString
	err := handle.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&name)
	if err != nil {
		t.Fatalf("asking whether the table %q exists: %v", table, err)
	}
	return name.Valid
}
