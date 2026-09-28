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
)

// These cases hold Connection to what it promises whatever the engine: the
// refusals, fn's error, the pinned session, and what happens to a connection
// whose lock-wait setting cannot be set or put back. What each engine's bound
// does to its lock waits is held by that engine's own cases, on the handle its
// module delivers.

// assembledWith runs the module's New for spec over a SQLite database at dsn,
// with the given items configured, and returns the product it built.
func assembledWith(t *testing.T, spec Spec, dsn string, items ...string) *database {
	t.Helper()
	product, err := spec.newDatabase(t.Context(), readerFor(t, spec, sectionBody(dsn, items...)), nil)
	if err != nil {
		t.Fatalf("assembling over %s: %v", sectionBody(dsn, items...), err)
	}
	t.Cleanup(func() { _ = Close(product.DB()) })
	return product
}

// openConnections is how many connections the pool behind a handle has open.
func openConnections(t *testing.T, handle *gorm.DB) int {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the pool: %v", err)
	}
	return pool.Stats().OpenConnections
}

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

// TestConnectionRefusesAHandleThisModuleDidNotOpen covers a handle opened
// around this module: nothing here knows how its engine bounds a lock wait.
func TestConnectionRefusesAHandleThisModuleDidNotOpen(t *testing.T) {
	foreign, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "foreign.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening a handle around this module: %v", err)
	}
	t.Cleanup(func() { _ = Close(foreign) })
	ran := false
	err = Connection(t.Context(), foreign, time.Second, func(*gorm.DB) error { ran = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "did not open") {
		t.Errorf("a handle this module did not open gave %v, want a refusal saying so", err)
	}
	if ran {
		t.Error("fn ran on a handle this module did not open")
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
		if _, pinned := session.Statement.ConnPool.(*sql.Conn); !pinned {
			t.Errorf("fn's session runs on %T, want the connection Connection took", session.Statement.ConnPool)
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
// context, one of its own settings, a transaction on it — and a connection
// built with Open, whose pool is its own: Connection takes a connection of the
// pool behind each.
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
	if err := Connection(t.Context(), product.DB(), time.Second, count); err == nil {
		t.Error("Connection from the delivered handle reached the second connection's table")
	}
	sessions := map[string]*gorm.DB{
		"WithContext": product.DB().WithContext(t.Context()),
		"Session":     product.DB().Session(&gorm.Session{SkipHooks: true}),
	}
	for name, session := range sessions {
		if err := Connection(t.Context(), session, time.Second, func(s *gorm.DB) error {
			return s.Exec("SELECT 1").Error
		}); err != nil {
			t.Errorf("Connection from a %s session of the handle: %v", name, err)
		}
	}
	if err := product.DB().Transaction(func(tx *gorm.DB) error {
		return Connection(t.Context(), tx, time.Second, func(s *gorm.DB) error { return s.Exec("SELECT 1").Error })
	}); err != nil {
		t.Errorf("Connection from a transaction on the handle: %v", err)
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

// TestConnectionBoundsTheLockWaitToWhatRemainsAndPutsItBack covers the order of
// the engine's two calls around fn: the bound, positive and at most the wait,
// goes on before fn runs, and what puts it back runs once fn has returned.
func TestConnectionBoundsTheLockWaitToWhatRemainsAndPutsItBack(t *testing.T) {
	var steps []string
	var bound time.Duration
	spec := sqliteSpec()
	spec.BoundLockWait = func(_ context.Context, _ *sql.Conn, d time.Duration) (func(context.Context) error, error) {
		steps = append(steps, "bound")
		bound = d
		return func(context.Context) error { steps = append(steps, "put back"); return nil }, nil
	}
	product := assembledWith(t, spec, filepath.Join(t.TempDir(), "steps.db"))

	const wait = 2 * time.Second
	if err := Connection(t.Context(), product.DB(), wait, func(*gorm.DB) error {
		steps = append(steps, "fn")
		return nil
	}); err != nil {
		t.Fatalf("Connection: %v", err)
	}
	if got := strings.Join(steps, ", "); got != "bound, fn, put back" {
		t.Errorf("the steps ran as %q, want the bound, fn, and the setting put back", got)
	}
	if bound <= 0 || bound > wait {
		t.Errorf("the lock wait was bounded to %v, want at most the wait of %v and more than nothing", bound, wait)
	}
}

// TestAConnectionWhoseSettingIsNotPutBackIsClosed covers the connection that
// would otherwise go back to the pool carrying the bound, cutting every later
// statement's lock wait short: it is closed instead. The first half is the
// control — a setting put back leaves the connection open in the pool.
func TestAConnectionWhoseSettingIsNotPutBackIsClosed(t *testing.T) {
	for _, c := range []struct {
		name    string
		putBack error
		open    int
	}{
		{"put back", nil, 1},
		{"not put back", errors.New("the setting could not be put back"), 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := sqliteSpec()
			spec.BoundLockWait = func(context.Context, *sql.Conn, time.Duration) (func(context.Context) error, error) {
				return func(context.Context) error { return c.putBack }, nil
			}
			product := assembledWith(t, spec, filepath.Join(t.TempDir(), "put-back.db"))
			if err := Connection(t.Context(), product.DB(), time.Second, func(s *gorm.DB) error {
				return s.Exec("SELECT 1").Error
			}); err != nil {
				t.Fatalf("Connection: %v", err)
			}
			if open := openConnections(t, product.DB()); open != c.open {
				t.Errorf("the pool has %d connections open afterwards, want %d", open, c.open)
			}
		})
	}
}

// TestAConnectionWhoseBoundCannotBeSetIsNotUsed covers the bound failing to go
// on: fn does not run on a connection whose lock waits are not bounded, the
// failure is reported, and the connection is closed rather than handed back in
// whatever state the attempt left it.
func TestAConnectionWhoseBoundCannotBeSetIsNotUsed(t *testing.T) {
	spec := sqliteSpec()
	spec.BoundLockWait = func(context.Context, *sql.Conn, time.Duration) (func(context.Context) error, error) {
		return nil, errors.New("the setting could not be read")
	}
	product := assembledWith(t, spec, filepath.Join(t.TempDir(), "unbounded.db"))
	ran := false
	err := Connection(t.Context(), product.DB(), time.Second, func(*gorm.DB) error { ran = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("Connection gave %v, want the failure to bound the lock wait", err)
	}
	if ran {
		t.Error("fn ran on a connection whose lock wait was not bounded")
	}
	if open := openConnections(t, product.DB()); open != 0 {
		t.Errorf("the pool has %d connections open afterwards, want the one taken closed", open)
	}
}
