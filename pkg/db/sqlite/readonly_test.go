package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
)

// readOnlyDatabase writes a database file holding one row in table t, closes
// it, and returns its path.
func readOnlyDatabase(t *testing.T, dir, name string) string {
	t.Helper()
	file := filepath.Join(dir, name)
	handle := open(t, file)
	if err := handle.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("creating the table in %s: %v", file, err)
	}
	if err := handle.Exec("INSERT INTO t (id) VALUES (1)").Error; err != nil {
		t.Fatalf("writing the row in %s: %v", file, err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool on %s: %v", file, err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("closing %s: %v", file, err)
	}
	return file
}

// TestATransactionOnADatabaseItsConnectionsCannotWriteRuns holds every shape
// of a locator whose connections cannot write to the transactions this engine
// begins on it: each one runs and reads, and a write in it is refused by
// SQLite as the connection refuses every write, rather than the BEGIN failing.
// SQLite begins an IMMEDIATE transaction on a database it opened read-only —
// mode=ro, immutable=1, a file the process may not write — as a read
// transaction, and refuses one on a connection whose query_only is on, which
// this engine therefore begins DEFERRED whatever mode the locator names.
func TestATransactionOnADatabaseItsConnectionsCannotWriteRuns(t *testing.T) {
	dir := t.TempDir()
	instance := deliver(t, filepath.Join(dir, "delivered.db"))
	file := readOnlyDatabase(t, dir, "read-only.db")
	unwritable := readOnlyDatabase(t, dir, "unwritable.db")
	if err := os.Chmod(unwritable, 0o444); err != nil {
		t.Fatalf("taking the write permission off %s: %v", unwritable, err)
	}
	unwritableSkipped := ""
	if f, err := os.OpenFile(unwritable, os.O_RDWR, 0); err == nil {
		_ = f.Close()
		unwritableSkipped = "this process writes a file whatever its permissions say"
	}

	cases := []struct {
		name, dsn, skip string
	}{
		{"query_only turned on", file + "?_pragma=query_only(1)", ""},
		{"query_only turned on in another spelling", file + "?_pragma=QUERY_ONLY=true", ""},
		{"query_only beside an immediate _txlock", file + "?_pragma=query_only(1)&_txlock=immediate", ""},
		{"query_only beside an exclusive _txlock", file + "?_pragma=query_only(1)&_txlock=Exclusive", ""},
		{"query_only beside _time_format", file + "?_time_format=sqlite&_pragma=query_only(1)", ""},
		{"a file: URI turning query_only on", "file:" + file + "?_pragma=query_only(1)", ""},
		{"mode=ro", "file:" + file + "?mode=ro", ""},
		{"mode=ro beside an exclusive _txlock", "file:" + file + "?mode=ro&_txlock=exclusive", ""},
		{"immutable=1", "file:" + file + "?immutable=1", ""},
		{"a file the process may not write", unwritable, unwritableSkipped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skip != "" {
				t.Skip(c.skip)
			}
			handle, err := instance.Open(context.Background(), c.dsn)
			if err != nil {
				t.Fatalf("opening %q: %v", c.dsn, err)
			}
			t.Cleanup(func() { _ = db.Close(handle) })
			readsAndRefusesWrites(t, handle.Session(&gorm.Session{Logger: logger.Discard}), nil)
			readsAndRefusesWrites(t, handle.Session(&gorm.Session{Logger: logger.Discard}),
				&sql.TxOptions{ReadOnly: true})
		})
	}
}

// TestATransactionOnAConnectionAStatementTurnedQueryOnlyRuns covers the one
// shape no locator gives: a statement turning query_only on for the connection
// it runs on. A transaction begun on that connection afterwards runs as one
// on a locator turning it on does.
func TestATransactionOnAConnectionAStatementTurnedQueryOnlyRuns(t *testing.T) {
	dir := t.TempDir()
	instance := deliver(t, filepath.Join(dir, "delivered.db"))
	file := readOnlyDatabase(t, dir, "turned.db")
	handle, err := instance.Open(context.Background(), file)
	if err != nil {
		t.Fatalf("opening %s: %v", file, err)
	}
	t.Cleanup(func() { _ = db.Close(handle) })
	quiet := handle.Session(&gorm.Session{Logger: logger.Discard})
	err = db.Connection(context.Background(), quiet, time.Second, func(conn *gorm.DB) error {
		if turned := conn.Exec("PRAGMA query_only = ON").Error; turned != nil {
			return turned
		}
		readsAndRefusesWrites(t, conn, nil)
		return conn.Exec("PRAGMA query_only = OFF").Error
	})
	if err != nil {
		t.Fatalf("running on one connection of %s: %v", file, err)
	}
}

// readsAndRefusesWrites runs a transaction on handle that reads table t's one
// row and then tries to write it, and fails the case unless the transaction
// began, read the row, and had the write refused as a write to a database the
// connection cannot write.
func readsAndRefusesWrites(t *testing.T, handle *gorm.DB, options *sql.TxOptions) {
	t.Helper()
	var rows int64
	var refused error
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT count(*) FROM t").Scan(&rows).Error; err != nil {
			return err
		}
		refused = tx.Exec("INSERT INTO t (id) VALUES (2)").Error
		return nil
	}, options)
	if err != nil {
		t.Fatalf("a transaction (options %+v) that reads gave %v, want it to run", options, err)
	}
	if rows != 1 {
		t.Errorf("the transaction (options %+v) read %d rows, want the 1 in the file", options, rows)
	}
	if refused == nil || !strings.Contains(refused.Error(), "readonly database") {
		t.Errorf("a write in the transaction (options %+v) gave %v, want SQLite's refusal of a write "+
			"to a database the connection cannot write", options, refused)
	}
}
