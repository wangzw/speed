package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These cases hold Connection to what it promises whatever the engine: the
// refusals, fn's error, the pinned session, and the handles it takes a
// connection from. What each engine does to a statement's lock wait, what a
// panic in fn leaves behind, and a transaction's savepoints are held by that
// engine's own cases, on the handle its module delivers.

// TestConnectionRefusesAWaitThatIsNotPositive covers the bound itself: a wait
// of zero or less would give up before waiting at all, and fn does not run.
func TestConnectionRefusesAWaitThatIsNotPositive(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t))
	for _, wait := range []time.Duration{0, -time.Second} {
		ran := false
		err := Connection(t.Context(), product.DB(), wait, func(*gorm.DB) error { ran = true; return nil })
		if err == nil || !strings.Contains(err.Error(), "positive") {
			t.Errorf("a wait of %v gave %v, want a refusal saying the wait has to be positive", wait, err)
		}
		if ran {
			t.Errorf("fn ran under a wait of %v", wait)
		}
	}
}

// TestConnectionRefusesNoHandle covers a nil handle, which has no pool to take
// a connection from and no session to run fn on.
func TestConnectionRefusesNoHandle(t *testing.T) {
	ran := false
	err := Connection(t.Context(), nil, time.Second, func(*gorm.DB) error { ran = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "no handle") {
		t.Errorf("a nil handle gave %v, want a refusal saying there was none", err)
	}
	if ran {
		t.Error("fn ran with no handle")
	}
}

// TestConnectionTakesAConnectionOfAHandleThisModuleDidNotOpen covers a handle a
// host opened itself with gorm.Open: Connection takes its connection from that
// handle's pool, and fn's statements run on it.
func TestConnectionTakesAConnectionOfAHandleThisModuleDidNotOpen(t *testing.T) {
	foreign, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "foreign.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening a handle around this module: %v", err)
	}
	t.Cleanup(func() { _ = Close(foreign) })
	var rows int64
	err = Connection(t.Context(), foreign, time.Second, func(session *gorm.DB) error {
		if _, pooled := session.Statement.ConnPool.(*sql.DB); pooled {
			t.Errorf("fn's session runs on the pool, want the connection Connection took")
		}
		for _, statement := range []string{
			"CREATE TABLE foreign_rows (id INTEGER PRIMARY KEY)",
			"INSERT INTO foreign_rows (id) VALUES (1)",
		} {
			if failed := session.Exec(statement).Error; failed != nil {
				return failed
			}
		}
		return session.Raw("SELECT count(*) FROM foreign_rows").Scan(&rows).Error
	})
	if err != nil || rows != 1 {
		t.Errorf("Connection on a handle this module did not open gave %v and read %d rows, want fn run "+
			"on a connection of its pool", err, rows)
	}
}

// namingPool is a host's wrapper of its pool that names the pool it wraps
// through gorm.GetDBConnector.
type namingPool struct{ *sql.DB }

// GetDBConn is the pool the wrapper wraps.
func (p namingPool) GetDBConn() (*sql.DB, error) { return p.DB, nil }

// opaquePool is a host's wrapper of its pool that names no pool.
type opaquePool struct{ *sql.DB }

// TestConnectionReadsAWrappedPoolOnlyWhenItNamesThePool covers a handle whose
// ConnPool is a host's wrapper of a pool. One answering gorm.GetDBConnector is
// a handle on the pool it names, and fn runs on a connection taken from it;
// one answering neither that nor *sql.DB gives Connection no pool to take a
// connection from, and fn runs on the handle itself, with nothing bounding the
// wait for a connection.
func TestConnectionReadsAWrappedPoolOnlyWhenItNamesThePool(t *testing.T) {
	pool, err := sql.Open(sqlite.DriverName, filepath.Join(t.TempDir(), "wrapped.db"))
	if err != nil {
		t.Fatalf("opening the pool the wrappers wrap: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	for _, c := range []struct {
		name   string
		conn   gorm.ConnPool
		pinned bool
	}{
		{"a wrapper answering GetDBConn", namingPool{pool}, true},
		{"a wrapper answering neither", opaquePool{pool}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			handle, err := gorm.Open(sqlite.Dialector{Conn: c.conn}, &gorm.Config{Logger: logger.Discard})
			if err != nil {
				t.Fatalf("opening a handle on the wrapper: %v", err)
			}
			var pinned bool
			if err := Connection(t.Context(), handle, time.Second, func(session *gorm.DB) error {
				_, pinned = session.Statement.ConnPool.(*pinnedConn)
				return session.Exec("SELECT 1").Error
			}); err != nil {
				t.Fatalf("Connection on the handle: %v", err)
			}
			if pinned != c.pinned {
				t.Errorf("fn ran on a connection Connection took: %v, want %v", pinned, c.pinned)
			}
		})
	}
}

// TestConnectionHandsFnsErrorBackAsItIs covers fn's own failure: a caller
// classifies it as it would the statement's, so it arrives unwrapped.
func TestConnectionHandsFnsErrorBackAsItIs(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t))
	failure := errors.New("the statement fn ran failed")
	err := Connection(t.Context(), product.DB(), time.Second, func(*gorm.DB) error { return failure })
	if !errors.Is(err, failure) || err.Error() != failure.Error() {
		t.Errorf("Connection gave %v, want fn's own error as it was returned", err)
	}
}

// TestConnectionPinsTheSessionToTheConnectionItTook covers the session fn is
// handed on a pool of one connection: its statements and a transaction begun
// from it run on the connection Connection holds. A session that went back to
// the pool would wait for that connection until its context ended.
func TestConnectionPinsTheSessionToTheConnectionItTook(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), `"max-open-conns":1`, `"max-idle-conns":1`)
	bounded, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := Connection(bounded, product.DB(), time.Second, func(session *gorm.DB) error {
		if _, pooled := session.Statement.ConnPool.(*sql.DB); pooled {
			t.Errorf("fn's session runs on the pool, want the connection Connection took")
		}
		if err := session.Exec("CREATE TABLE pinned (id INTEGER PRIMARY KEY)").Error; err != nil {
			return err
		}
		return session.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
			return tx.Exec("INSERT INTO pinned (id) VALUES (1)").Error
		})
	})
	if err != nil {
		t.Fatalf("statements and a transaction on fn's session: %v", err)
	}
	var rows int64
	if err := product.DB().Raw("SELECT count(*) FROM pinned").Scan(&rows).Error; err != nil || rows != 1 {
		t.Errorf("the handle reads %d rows with %v after the transaction, want the one it wrote", rows, err)
	}
}

// TestConnectionTakesFromThePoolBehindAnySessionOfTheHandle covers the
// sessions a caller may hold instead of the handle itself — one with its own
// context, one of its own settings — and a connection built with Open, whose
// pool is its own: Connection takes a connection of the pool behind each.
func TestConnectionTakesFromThePoolBehindAnySessionOfTheHandle(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t))
	opened, err := product.Open(t.Context(), filepath.Join(t.TempDir(), "opened.db"))
	if err != nil {
		t.Fatalf("opening a second connection: %v", err)
	}
	t.Cleanup(func() { _ = Close(opened) })
	if err := opened.Exec("CREATE TABLE only_opened (id INTEGER)").Error; err != nil {
		t.Fatalf("creating a table on the second connection: %v", err)
	}

	count := func(session *gorm.DB) error {
		var rows int64
		return session.Raw("SELECT count(*) FROM only_opened").Scan(&rows).Error
	}
	if err := Connection(t.Context(), opened.WithContext(t.Context()), time.Second, count); err != nil {
		t.Errorf("Connection from a session of the second connection: %v", err)
	}
	quiet := product.DB().Session(&gorm.Session{Logger: logger.Discard})
	if err := Connection(t.Context(), quiet, time.Second, count); err == nil {
		t.Error("Connection from the delivered handle reached the second connection's table")
	}
	sessions := map[string]*gorm.DB{
		"WithContext": product.DB().WithContext(t.Context()),
		"Session":     product.DB().Session(&gorm.Session{SkipHooks: true}),
	}
	for name, session := range sessions {
		if err := Connection(t.Context(), session, time.Second, func(s *gorm.DB) error {
			if _, pooled := s.Statement.ConnPool.(*sql.DB); pooled {
				t.Errorf("fn's session from a %s session runs on the pool, want a connection of it", name)
			}
			return s.Exec("SELECT 1").Error
		}); err != nil {
			t.Errorf("Connection from a %s session of the handle: %v", name, err)
		}
	}
}

// TestConnectionRunsFnOnTheSessionItPinned covers Connection handed fn's own
// session: that session is on one connection already, the one Connection took,
// so fn runs on it rather than waiting for a second connection of a pool that
// may have none.
func TestConnectionRunsFnOnTheSessionItPinned(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), `"max-open-conns":1`, `"max-idle-conns":1`)
	err := Connection(t.Context(), product.DB(), time.Second, func(outer *gorm.DB) error {
		return Connection(t.Context(), outer, 100*time.Millisecond, func(inner *gorm.DB) error {
			if inner.Statement.ConnPool != outer.Statement.ConnPool {
				t.Error("fn's session inside Connection on fn's own session is on another connection")
			}
			return inner.Exec("SELECT 1").Error
		})
	})
	if err != nil {
		t.Errorf("Connection on the session Connection pinned gave %v, want fn run on it", err)
	}
}

// TestAStatementThroughConnectionRunsTheHandlesCallbacks covers what the
// session carries: the callbacks installed on the handle — the declared
// plugins' and this module's own — run on its statements, so a write through
// it into a sealed column is sealed.
func TestAStatementThroughConnectionRunsTheHandlesCallbacks(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	const email = "through-connection@example.com"
	row := encryptedModel{Email: email}
	if err := Connection(t.Context(), product.DB(), time.Second, func(session *gorm.DB) error {
		return session.Create(&row).Error
	}); err != nil {
		t.Fatalf("writing through Connection: %v", err)
	}
	assertSealed(t, product, row.ID, email)
}
