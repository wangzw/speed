package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Connection runs fn, under ctx, on one connection of the handle's pool, taken
// within wait.
//
// It is for statements a module issues on its own account: a plugin writing a
// row of its own, a subscriber reading a table, a call that reads through a
// handle on behalf of whoever made it. Such a statement can be reached from a
// caller that already holds connections of the same pool — inside its
// transaction on the handle, or while it iterates a result set of it — and a
// pool with none left over then has the statement wait for the caller, which
// waits for the statement. A handle held to one connection is always in that
// position while its caller holds the connection; a larger pool is in it once
// every connection is held that way. The wait ends with the statement's context
// and, without a deadline on it, never. Taken through here, the connection is
// waited for no longer than wait, and the failure says what held it.
//
// Only the wait for the connection is bounded. fn's statements run under ctx
// and wait for a lock another connection holds as the engine makes them wait,
// with whatever the connection carries: on SQLite, the connection's busy
// timeout, which the locator sets and an ending context does not cut short; on
// PostgreSQL, until ctx ends or the session's lock_timeout or
// statement_timeout, set by the locator or the server, ends the wait first,
// and with neither for as long as the lock is held. Nothing here reads or
// changes those settings.
//
// The session fn receives is pinned to the connection, and so is everything
// derived from it, a transaction included; its Statement.ConnPool is this
// package's wrapper of the connection, not the *sql.Conn. When fn returns, or
// panics, every transaction it left open on the connection is rolled back and
// every result set it left open is closed, and the connection goes back to the
// pool; a panic then carries on up to the caller. fn's own error is returned as
// it is.
//
// A handle that is not on a pool has no connection to hand out: a transaction,
// or a session already pinned to one connection, fn's own among them. fn then
// runs on the handle itself, under ctx, and nothing is waited for. A handle on
// a pool is any other, whether this module opened it or not.
//
// No connection within wait reports an error that wraps
// context.DeadlineExceeded while ctx itself has not ended; that is how a caller
// tells the bound from its own context. A wait that is not positive is refused,
// and so is a nil handle.
func Connection(ctx context.Context, handle *gorm.DB, wait time.Duration, fn func(session *gorm.DB) error) error {
	if wait <= 0 {
		return fmt.Errorf("db: Connection was given a wait of %v, and a bound on waiting has to be positive", wait)
	}
	if handle == nil || handle.Config == nil {
		return errors.New("db: Connection was handed no handle to take a connection from")
	}
	session := handle.Session(&gorm.Session{NewDB: true, Context: ctx})
	pool, pooled := pooledOn(handle)
	if !pooled {
		return fn(session)
	}
	taking, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	conn, err := pool.Conn(taking)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("db: no connection of the pool came free within %v; on a database held to "+
				"one connection, a transaction or a result set on the same handle holds it, and waiting "+
				"longer would be waiting for whoever holds it to be done with this statement: %w",
				wait, err)
		}
		return fmt.Errorf("db: taking a connection of the pool: %w", err)
	}
	released, release := context.WithCancel(context.Background())
	pinned := &pinnedConn{conn: conn, released: released}
	// Deferred, so that it runs on a panic too: nothing fn left open holds the
	// connection back from the pool, and closing it cannot wait for fn.
	defer func() {
		pinned.end()
		release()
		_ = conn.Close()
	}()
	session.Statement.ConnPool = pinned
	return fn(session)
}

// pooledOn is the pool a handle's statements take their connection from, or
// false for a handle already on one connection — a transaction, a session
// pinned to a connection — or on nothing this package knows to take one from.
func pooledOn(handle *gorm.DB) (*sql.DB, bool) {
	on := handle.ConnPool
	if handle.Statement != nil && handle.Statement.ConnPool != nil {
		on = handle.Statement.ConnPool
	}
	switch pool := on.(type) {
	case *sql.DB:
		return pool, pool != nil
	case gorm.TxCommitter:
		return nil, false
	case gorm.GetDBConnector:
		db, err := pool.GetDBConn()
		return db, err == nil && db != nil
	default:
		return nil, false
	}
}

// pinnedConn is the connection Connection took, as fn's session reaches it. It
// keeps what fn could leave open on the connection within reach of the call:
// each transaction begun on it, and each result set opened on it outside a
// transaction, whose context ends once released does.
//
// It answers GORM's TxBeginner rather than TxCommitter, so a session on it is
// outside any transaction and GORM begins one through BeginTx; the
// transaction's statements, its savepoints included, run on the *sql.Tx that
// returns, as they would on any other.
type pinnedConn struct {
	conn     *sql.Conn
	released context.Context

	// mu guards begun: fn may run statements on its session from more than
	// one goroutine, and database/sql serialises them on the connection.
	mu    sync.Mutex
	begun []*sql.Tx
}

// PrepareContext prepares a statement on the connection.
func (p *pinnedConn) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return p.conn.PrepareContext(ctx, query)
}

// ExecContext runs a statement on the connection.
func (p *pinnedConn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.conn.ExecContext(ctx, query, args...)
}

// QueryContext runs a query on the connection.
func (p *pinnedConn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.conn.QueryContext(p.untilReleased(ctx), query, args...)
}

// QueryRowContext runs a query for one row on the connection.
func (p *pinnedConn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.conn.QueryRowContext(p.untilReleased(ctx), query, args...)
}

// BeginTx begins a transaction on the connection.
func (p *pinnedConn) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	tx, err := p.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.begun = append(p.begun, tx)
	p.mu.Unlock()
	return tx, nil
}

// untilReleased is ctx, ended as well once the connection is released. A result
// set left open on the connection holds it until the set is closed, and
// database/sql closes a set whose context ends.
func (p *pinnedConn) untilReleased(ctx context.Context) context.Context {
	bounded, cancel := context.WithCancel(ctx)
	context.AfterFunc(p.released, cancel)
	return bounded
}

// end rolls back every transaction begun on the connection that is still open,
// on the connection it runs on and before the connection goes back; one
// already committed or rolled back is left as it is. Under a context that never
// ends database/sql rolls such a transaction back never, and the connection's
// release waits for it for good. Under one that ends it rolls it back on a
// goroutine of its own and, for a driver that cannot reset a session, asks for
// the connection to be dropped — on a database held to one connection, the
// database itself — whether it is turning on which of that goroutine and the
// release gets there first.
func (p *pinnedConn) end() {
	p.mu.Lock()
	begun := p.begun
	p.begun = nil
	p.mu.Unlock()
	for _, tx := range begun {
		_ = tx.Rollback()
	}
}

// engineMarkerName is the name the engine marker is installed under in a
// handle's plugin table. Like the mark Open installs, it is this package's own
// path with a suffix no declared plugin takes.
const engineMarkerName = "github.com/vislake/speed/pkg/db#engine"

// engineMarker is what every handle this package opens carries in its plugin
// table, ahead of the declared plugins: the engine behind the handle. It is how
// a callback handed nothing but a statement reads the statement's identifiers
// by that engine's rules (namesColumn) — from a transaction or any other
// session derived from the handle, since each of them shares the table.
type engineMarker struct {
	dialect Dialect
}

// Name is the marker's key in the plugin table.
func (*engineMarker) Name() string { return engineMarkerName }

// Initialize registers nothing: the marker is read, not run.
func (*engineMarker) Initialize(*gorm.DB) error { return nil }

// markEngine installs the engine marker on a handle openHandle opened.
func markEngine(session *gorm.DB, spec Spec) error {
	if err := session.Use(&engineMarker{dialect: spec.Dialect}); err != nil {
		return fmt.Errorf("db: marking a %s connection with its engine failed: %w", spec.Dialect, err)
	}
	return nil
}

// engineOf reads the engine marker of a session.
func engineOf(session *gorm.DB) (*engineMarker, bool) {
	if session == nil || session.Config == nil {
		return nil, false
	}
	marker, ok := session.Plugins[engineMarkerName].(*engineMarker)
	return marker, ok
}
