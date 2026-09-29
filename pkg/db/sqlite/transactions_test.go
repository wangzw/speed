package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestBeginStatementReadsTheLocatorTheWayTheDriverDocumentsIt pins the mode a
// transaction on each locator begins in: IMMEDIATE unless the locator's
// _txlock names another — the first one, in any letter case, whatever else the
// locator carries — or the locator opens its database read-only; and DEFERRED
// for a transaction begun read-only.
func TestBeginStatementReadsTheLocatorTheWayTheDriverDocumentsIt(t *testing.T) {
	cases := []struct {
		dsn       string
		readOnly  bool
		statement string
	}{
		{"/var/lib/app/app.db", false, "BEGIN IMMEDIATE"},
		{":memory:", false, "BEGIN IMMEDIATE"},
		{"", false, "BEGIN IMMEDIATE"},
		{"/var/lib/app/app.db?_pragma=foreign_keys(1)", false, "BEGIN IMMEDIATE"},
		{"/var/lib/app/app.db?_txlock=deferred", false, "BEGIN DEFERRED"},
		{"/var/lib/app/app.db?_txlock=Exclusive", false, "BEGIN EXCLUSIVE"},
		{"/var/lib/app/app.db?_txlock=immediate", false, "BEGIN IMMEDIATE"},
		{"file:/var/lib/app/app.db?_txlock=deferred", false, "BEGIN DEFERRED"},
		// The driver reads the first _txlock the query gives.
		{"/var/lib/app/app.db?_txlock=deferred&_txlock=exclusive", false, "BEGIN DEFERRED"},
		// The driver drops _txlock beside _time_format; this reading does not.
		{"/var/lib/app/app.db?_time_format=sqlite&_txlock=deferred", false, "BEGIN DEFERRED"},
		// A file: URI whose last mode is ro never writes.
		{"file:/var/lib/app/app.db?mode=ro", false, "BEGIN DEFERRED"},
		{"file:/var/lib/app/app.db?mode=ro&mode=rw", false, "BEGIN IMMEDIATE"},
		{"file:/var/lib/app/app.db?mode=rw&mode=ro", false, "BEGIN DEFERRED"},
		{"file:/var/lib/app/app.db#?mode=ro", false, "BEGIN IMMEDIATE"},
		{"file:/var/lib/app/app.db?mode=ro&_txlock=immediate", false, "BEGIN IMMEDIATE"},
		// Outside a file: URI the parameters are the driver's, and mode is none.
		{"/var/lib/app/app.db?mode=ro", false, "BEGIN IMMEDIATE"},
		// A transaction begun read-only is deferred, as the driver begins one.
		{"/var/lib/app/app.db", true, "BEGIN DEFERRED"},
		{"/var/lib/app/app.db?_txlock=exclusive", true, "BEGIN DEFERRED"},
	}
	for _, c := range cases {
		l, _ := readLocator(c.dsn)
		got, err := beginStatement(l, c.readOnly)
		if err != nil || got != c.statement {
			t.Errorf("beginStatement(%q, read-only %v) = %q, %v, want %q", c.dsn, c.readOnly, got, err, c.statement)
		}
	}
	l, _ := readLocator("/var/lib/app/app.db?_time_format=sqlite&_txlock=eventually")
	if _, err := beginStatement(l, false); err == nil || !strings.Contains(err.Error(), "_txlock") {
		t.Errorf("a _txlock the driver does not know gave %v, want it refused", err)
	}
}

// The locks a transaction on the first connection holds, as a second connection
// on the same file observes them.
const (
	holdsNothing   = "nothing"
	holdsWriteLock = "the write lock"
	holdsExclusive = "the database"
)

// TestATransactionBeginsInTheModeItsLocatorGives holds a transaction begun
// through this engine's driver to the lock it holds from its first statement
// on, observed from a second connection on the file that does not wait: a
// deferred transaction holds nothing yet, an immediate one the write lock and
// an exclusive one the whole database, which a reader cannot read either.
func TestATransactionBeginsInTheModeItsLocatorGives(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, parameters string
		options          *sql.TxOptions
		holds            string
	}{
		{"a locator naming no mode", "", nil, holdsWriteLock},
		{"a locator setting other parameters", "?_pragma=foreign_keys(1)", nil, holdsWriteLock},
		{"a locator naming deferred", "?_txlock=deferred", nil, holdsNothing},
		{"a locator naming exclusive", "?_txlock=exclusive", nil, holdsExclusive},
		{"deferred beside _time_format", "?_time_format=sqlite&_txlock=deferred", nil, holdsNothing},
		{"exclusive beside _time_format", "?_time_format=sqlite&_txlock=exclusive", nil, holdsExclusive},
		{"a transaction begun read-only", "", &sql.TxOptions{ReadOnly: true}, holdsNothing},
		{"a locator turning query_only on", "?_pragma=query_only(1)", nil, holdsNothing},
		{"query_only beside exclusive", "?_pragma=query_only(1)&_txlock=exclusive", nil, holdsNothing},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join(dir, strings.Repeat("x", i+1)+".db")
			if got := observeBegin(t, file, file+c.parameters, c.options); got != c.holds {
				t.Errorf("a transaction on %q holds %s once begun, want %s", c.parameters, got, c.holds)
			}
		})
	}
	file := filepath.Join(dir, "read-only.db")
	if got := observeBegin(t, file, "file:"+file+"?mode=ro", nil); got != holdsNothing {
		t.Errorf("a transaction on a read-only locator holds %s once begun, want %s", got, holdsNothing)
	}
}

// observeBegin creates the database file, begins a transaction on a pool of
// this engine's driver over dsn, and reports what a second connection on the
// file is then refused.
func observeBegin(t *testing.T, file, dsn string, options *sql.TxOptions) string {
	t.Helper()
	ctx := context.Background()
	observer, err := sql.Open(driverName, file+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("opening the observer on %s: %v", file, err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	observer.SetMaxOpenConns(1)
	if _, err = observer.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS observed (id INTEGER)"); err != nil {
		t.Fatalf("creating the table on %s: %v", file, err)
	}
	pool, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatalf("opening %q: %v", dsn, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		t.Fatalf("beginning a transaction on %q: %v", dsn, err)
	}
	defer func() { _ = tx.Rollback() }()

	var rows int64
	if err = observer.QueryRowContext(ctx, "SELECT count(*) FROM observed").Scan(&rows); err != nil {
		if !strings.Contains(err.Error(), "SQLITE_BUSY") {
			t.Fatalf("reading beside the transaction on %q: %v", dsn, err)
		}
		return holdsExclusive
	}
	if _, err = observer.ExecContext(ctx, "INSERT INTO observed (id) VALUES (1)"); err != nil {
		if !strings.Contains(err.Error(), "SQLITE_BUSY") {
			t.Fatalf("writing beside the transaction on %q: %v", dsn, err)
		}
		return holdsWriteLock
	}
	return holdsNothing
}

// TestABeginRefusedTheWriteLockIsReported holds the DEFERRED begin to the
// connections whose query_only is on: on any other connection a BEGIN SQLite
// refuses — here for the write lock another connection holds, with no busy
// timeout to wait it out — is the error the caller gets, and no transaction
// is begun in its place.
func TestABeginRefusedTheWriteLockIsReported(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "held.db")
	holder, err := sql.Open(driverName, file)
	if err != nil {
		t.Fatalf("opening the holder on %s: %v", file, err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if _, err = holder.ExecContext(ctx, "CREATE TABLE held (id INTEGER)"); err != nil {
		t.Fatalf("creating the table on %s: %v", file, err)
	}
	held, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("taking the write lock on %s: %v", file, err)
	}
	defer func() { _ = held.Rollback() }()

	pool, err := sql.Open(driverName, file+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("opening the pool on %s: %v", file, err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	tx, err := pool.BeginTx(ctx, nil)
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("a transaction began while another connection held the write lock, want the BEGIN refused")
	}
	if !strings.Contains(err.Error(), "SQLITE_BUSY") {
		t.Errorf("the refused BEGIN gave %v, want SQLite's SQLITE_BUSY", err)
	}
}

// stubConn is a driver connection as the SQLite driver's own behaves on a
// connection whose query_only is on: it refuses BEGIN IMMEDIATE with refused
// and runs every other statement, recording each one in the order it was run.
type stubConn struct {
	statements []string
	refused    error
}

func (*stubConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*stubConn) Close() error                        { return nil }
func (*stubConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (c *stubConn) ExecContext(_ context.Context, statement string, _ []driver.NamedValue) (driver.Result, error) {
	c.statements = append(c.statements, statement)
	if statement == "BEGIN IMMEDIATE" {
		return nil, c.refused
	}
	return driver.RowsAffected(0), nil
}

// answeringConn is a stubConn that answers a query, the way a connection
// answers PRAGMA query_only: with the one row it holds, with none, or with an
// error.
type answeringConn struct {
	*stubConn
	values []driver.Value
	err    error
}

func (c answeringConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.err != nil {
		return nil, c.err
	}
	return &stubRows{values: c.values}, nil
}

// stubRows is a result set of the one row a case gives it, or of no row.
type stubRows struct {
	values []driver.Value
	read   bool
}

func (*stubRows) Columns() []string { return []string{"query_only"} }
func (*stubRows) Close() error      { return nil }

func (r *stubRows) Next(dest []driver.Value) error {
	if r.read || len(r.values) == 0 {
		return io.EOF
	}
	r.read = true
	copy(dest, r.values)
	return nil
}

// TestARefusedBeginIsRetriedOnlyWhenTheConnectionSaysQueryOnlyIsOn holds the
// fallback for a BEGIN SQLite refused to the connection's answer about
// query_only: a connection that answers that it is on begins the transaction
// DEFERRED instead, and every other answer leaves the refusal as the error the
// caller gets, with no second BEGIN run. The answers that are not "on" are the
// connection that answers no query at all, the one whose read fails, the one
// that reads no row, the one that answers something other than a number, and
// the one that answers off.
func TestARefusedBeginIsRetriedOnlyWhenTheConnectionSaysQueryOnlyIsOn(t *testing.T) {
	refused := errors.New("attempt to write a readonly database (8)")
	locator, _ := readLocator("")
	cases := []struct {
		name string
		// answer wraps the stub the case's connection runs on; a stubConn as
		// it is answers no query, so the connection implements no
		// driver.QueryerContext.
		answer         func(*stubConn) driver.Conn
		wantStatements []string
		wantRefused    bool
	}{
		{
			name:           "a connection that answers no query",
			answer:         func(c *stubConn) driver.Conn { return c },
			wantStatements: []string{"BEGIN IMMEDIATE"},
			wantRefused:    true,
		},
		{
			name: "a connection whose query fails",
			answer: func(c *stubConn) driver.Conn {
				return answeringConn{stubConn: c, err: errors.New("the pragma could not be read")}
			},
			wantStatements: []string{"BEGIN IMMEDIATE"},
			wantRefused:    true,
		},
		{
			name:           "a connection that reads no row",
			answer:         func(c *stubConn) driver.Conn { return answeringConn{stubConn: c} },
			wantStatements: []string{"BEGIN IMMEDIATE"},
			wantRefused:    true,
		},
		{
			name: "a connection that answers something other than a number",
			answer: func(c *stubConn) driver.Conn {
				return answeringConn{stubConn: c, values: []driver.Value{"on"}}
			},
			wantStatements: []string{"BEGIN IMMEDIATE"},
			wantRefused:    true,
		},
		{
			name: "a connection that answers off",
			answer: func(c *stubConn) driver.Conn {
				return answeringConn{stubConn: c, values: []driver.Value{int64(0)}}
			},
			wantStatements: []string{"BEGIN IMMEDIATE"},
			wantRefused:    true,
		},
		{
			name: "a connection that answers on",
			answer: func(c *stubConn) driver.Conn {
				return answeringConn{stubConn: c, values: []driver.Value{int64(1)}}
			},
			wantStatements: []string{"BEGIN IMMEDIATE", "BEGIN DEFERRED"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := &stubConn{refused: refused}
			conn := c.answer(stub)
			wrapped := transactionConn{Conn: conn, execer: conn.(driver.ExecerContext), locator: locator}
			if _, err := wrapped.BeginTx(context.Background(), driver.TxOptions{}); c.wantRefused {
				if !errors.Is(err, refused) {
					t.Errorf("beginning the transaction gave %v, want the refusal of the BEGIN", err)
				}
			} else if err != nil {
				t.Errorf("beginning the transaction gave %v, want the DEFERRED BEGIN to run it", err)
			}
			if !slices.Equal(stub.statements, c.wantStatements) {
				t.Errorf("the connection ran %v, want %v", stub.statements, c.wantStatements)
			}
		})
	}
}

// TestALocatorWithATxlockTheDriverDoesNotKnowIsNotOpened covers the one
// reading of _txlock that is refused: a value the driver does not know, which
// the driver itself lets through beside _time_format.
func TestALocatorWithATxlockTheDriverDoesNotKnowIsNotOpened(t *testing.T) {
	dir := t.TempDir()
	for _, parameters := range []string{"?_txlock=eventually", "?_time_format=sqlite&_txlock=eventually"} {
		pool, err := sql.Open(driverName, filepath.Join(dir, "refused.db")+parameters)
		if err != nil {
			t.Fatalf("opening the pool: %v", err)
		}
		err = pool.PingContext(context.Background())
		_ = pool.Close()
		if err == nil || !strings.Contains(err.Error(), `unknown _txlock "eventually"`) {
			t.Errorf("a connection on %q gave %v, want the _txlock refused", parameters, err)
		}
	}
}
