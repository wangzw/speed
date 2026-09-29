package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// Connection runs fn on one connection of the handle's pool, taken within
// wait, and holds every wait of fn's statements for a lock another connection
// holds to what is left of wait when the statement begins: the wait for the
// connection and every lock wait of fn's together end once wait has passed.
//
// It is for statements a module issues on its own account on a handle this
// module opened: a plugin writing a row of its own, a subscriber reading a
// table, a call that reads through the delivered handle on behalf of whoever
// made it. Such a statement can be reached from a caller that already holds
// connections of the same pool — inside its transaction on the handle, or
// while it iterates a result set of it — and a pool with none left over then
// has the statement wait for the caller, which waits for the statement. A
// handle held to one connection is always in that position while its caller
// holds the connection; a larger pool is in it once every connection is held
// that way. The wait ends with the statement's context and, without a deadline
// on it, never. Taken through here, the connection is waited for no longer
// than wait, and the failure says what held it.
//
// The bound on lock waits is set on the connection rather than carried by a
// context, because a context does not end every lock wait: SQLite waits for a
// lock inside the driver, for as long as the connection's busy timeout, and
// ending the statement's context meanwhile does not cut that wait short.
// PostgreSQL ends a wait with the context, and bounds one per statement with
// its lock timeout, which is what is set there. A lock wait that runs past the
// bound fails its statement with the engine's own error for it — SQLite's
// SQLITE_BUSY, PostgreSQL's lock_not_available. The statements themselves run
// under ctx: a statement under way is not cut short by the bound.
//
// The lock-wait bound is set on the connection once it is taken, to what is
// left of wait, and set again before each later statement fn issues — the
// begin and the commit of a transaction included — once a whole millisecond of
// it has gone: the engines hold each statement, and on PostgreSQL each lock a
// statement waits for, to the setting in force when it begins, so a bound set
// once would give every statement the whole of it again. What is left is
// rounded up to the millisecond both engines take it in, so a lock wait the
// bound cuts short ends no sooner than wait after the call began: a caller that
// measures from there tells a statement the bound failed from one that failed
// before it. Past that point a statement that meets a lock fails within a
// millisecond of it; one that meets none runs as it would have.
//
// The session fn receives is pinned to the connection, and so is everything
// derived from it, a transaction included. fn has to be done with it when it
// returns — every result set closed, every transaction ended — because the
// connection then goes back to the pool with the lock-wait setting it had
// before; a connection whose setting cannot be put back is closed instead of
// being handed on with the bound. fn's own error is returned as it is, and a
// statement whose bound could not be set again fails with that failure without
// being run.
//
// No connection within wait reports an error that wraps
// context.DeadlineExceeded while ctx itself has not ended; that is how a caller
// tells the bound from its own context. A wait that is not positive is
// refused, and so is a handle this module did not open: nothing here knows how
// to bound that handle's lock waits.
func Connection(ctx context.Context, handle *gorm.DB, wait time.Duration, fn func(session *gorm.DB) error) error {
	if wait <= 0 {
		return fmt.Errorf("db: Connection was given a wait of %v, and a bound on waiting has to be positive", wait)
	}
	engine, ok := engineOf(handle)
	if !ok {
		return errors.New("db: Connection was handed a handle this module did not open, so how long its " +
			"statements wait for a lock is nothing this module can bound: use the delivered handle, a " +
			"session derived from it, or a connection built with Open")
	}
	taking, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	conn, err := engine.pool.Conn(taking)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("db: no connection of the %s pool came free within %v; on a database held to "+
				"one connection, a transaction or a result set on the same handle holds it, and waiting "+
				"longer would be waiting for whoever holds it to be done with this statement: %w",
				engine.dialect, wait, err)
		}
		return fmt.Errorf("db: taking a connection of the %s pool: %w", engine.dialect, err)
	}
	deadline, _ := taking.Deadline()

	// The setting is read, bounded and put back outside ctx's cancellation,
	// so that a caller giving up does not leave a connection carrying the
	// bound; each of those statements is bounded by wait on its own.
	setting := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), wait)
	}
	bound := &lockBound{engine: engine, conn: conn, deadline: deadline, setting: setting}
	settle, done := setting()
	first := lockWaitBound(time.Until(deadline))
	restore, err := engine.boundLockWait(settle, conn, first)
	done()
	if err != nil {
		dropFromPool(conn)
		return bound.failed(err)
	}
	bound.inForce = first
	defer func() {
		settle, done := setting()
		defer done()
		if restore(settle) != nil {
			dropFromPool(conn)
			return
		}
		_ = conn.Close()
	}()

	session := handle.Session(&gorm.Session{NewDB: true, Context: ctx})
	session.Statement.ConnPool = &boundedConn{bound: bound}
	return fn(session)
}

// lockBound is the lock-wait bound of the connection Connection took: the
// deadline the wait ends at, and the bound in force on the connection.
type lockBound struct {
	engine   *engineMarker
	conn     *sql.Conn
	deadline time.Time
	setting  func() (context.Context, context.CancelFunc)

	// mu guards inForce: fn may run statements on its session from more than
	// one goroutine, and database/sql serialises them on the connection.
	mu      sync.Mutex
	inForce time.Duration
}

// tighten sets the connection's lock-wait bound to what is left of the wait,
// before a statement, when that has fallen below the bound in force.
func (b *lockBound) tighten() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	left := lockWaitBound(time.Until(b.deadline))
	if left >= b.inForce {
		return nil
	}
	settle, done := b.setting()
	defer done()
	// What the engine hands back puts back the setting it read, which is the
	// bound set before; the one put back when fn returns is the first.
	if _, err := b.engine.boundLockWait(settle, b.conn, left); err != nil {
		return b.failed(err)
	}
	b.inForce = left
	return nil
}

// failed is a failure to set the connection's lock-wait bound.
func (b *lockBound) failed(err error) error {
	return fmt.Errorf("db: bounding how long a connection of the %s pool waits for a lock: %w",
		b.engine.dialect, err)
}

// boundedConn is the connection Connection pins fn's session to: each statement
// on it, and the transaction begun on it, first sets the connection's lock-wait
// bound to what is left of the wait.
//
// It answers GORM's ConnPoolBeginner rather than TxCommitter, so a session on it
// is outside any transaction and GORM begins one through BeginTx.
type boundedConn struct{ bound *lockBound }

// PrepareContext prepares a statement on the connection.
func (c *boundedConn) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if err := c.bound.tighten(); err != nil {
		return nil, err
	}
	return c.bound.conn.PrepareContext(ctx, query)
}

// ExecContext runs a statement on the connection.
func (c *boundedConn) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := c.bound.tighten(); err != nil {
		return nil, err
	}
	return c.bound.conn.ExecContext(ctx, query, args...)
}

// QueryContext runs a query on the connection.
func (c *boundedConn) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := c.bound.tighten(); err != nil {
		return nil, err
	}
	return c.bound.conn.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a query for one row on the connection. A bound that
// could not be set comes back as the row's error, which is the only error a
// *sql.Row carries.
func (c *boundedConn) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if err := c.bound.tighten(); err != nil {
		return refusedRow(err)
	}
	return c.bound.conn.QueryRowContext(ctx, query, args...)
}

// BeginTx begins a transaction on the connection.
func (c *boundedConn) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if err := c.bound.tighten(); err != nil {
		return nil, err
	}
	tx, err := c.bound.conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &boundedTx{tx: tx, bound: c.bound}, nil
}

// boundedTx is a transaction on the connection Connection took, each of whose
// statements, and its commit, first sets the connection's lock-wait bound to
// what is left of the wait. The bound is set on the connection itself, which is
// the one the transaction runs on.
type boundedTx struct {
	tx    *sql.Tx
	bound *lockBound
}

// PrepareContext prepares a statement in the transaction.
func (t *boundedTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	if err := t.bound.tighten(); err != nil {
		return nil, err
	}
	return t.tx.PrepareContext(ctx, query)
}

// ExecContext runs a statement in the transaction.
func (t *boundedTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := t.bound.tighten(); err != nil {
		return nil, err
	}
	return t.tx.ExecContext(ctx, query, args...)
}

// QueryContext runs a query in the transaction.
func (t *boundedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := t.bound.tighten(); err != nil {
		return nil, err
	}
	return t.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext runs a query for one row in the transaction.
func (t *boundedTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if err := t.bound.tighten(); err != nil {
		return refusedRow(err)
	}
	return t.tx.QueryRowContext(ctx, query, args...)
}

// Commit commits the transaction. SQLite's commit waits for the lock it writes
// under, so it is bounded as a statement is; a bound that cannot be set ends the
// transaction with a rollback instead, so that the connection is not handed back
// inside it.
func (t *boundedTx) Commit() error {
	if err := t.bound.tighten(); err != nil {
		return errors.Join(err, t.tx.Rollback())
	}
	return t.tx.Commit()
}

// Rollback rolls the transaction back, which waits for no lock.
func (t *boundedTx) Rollback() error { return t.tx.Rollback() }

// lockWaitBound is the bound a connection's lock waits are held to when
// remaining is what is left of the wait: rounded up to a whole millisecond, the
// unit both engines take it in, so that the engine's own rounding never ends a
// lock wait before the wait has passed; and at least one, because zero turns
// either engine's bound off rather than making it immediate.
func lockWaitBound(remaining time.Duration) time.Duration {
	whole := (remaining + time.Millisecond - 1) / time.Millisecond * time.Millisecond
	return max(whole, time.Millisecond)
}

// dropFromPool closes a connection taken from a pool instead of handing it
// back: database/sql closes the connection underneath when a use of it reports
// driver.ErrBadConn, and the Conn is done after that.
func dropFromPool(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// engineMarkerName is the name the engine marker is installed under in a
// handle's plugin table. Like the mark Open installs, it is this package's own
// path with a suffix no declared plugin takes.
const engineMarkerName = "github.com/vislake/speed/pkg/db#engine"

// engineMarker is what every handle this package opens carries in its plugin
// table, ahead of the declared plugins: the pool behind the handle, and how its
// engine bounds a connection's lock waits. Connection reads it, which is how a
// function handed nothing but a session finds both — from a transaction or any
// other session derived from the handle, since each of them shares the table.
type engineMarker struct {
	dialect       Dialect
	pool          *sql.DB
	boundLockWait func(ctx context.Context, conn *sql.Conn, bound time.Duration) (func(context.Context) error, error)
}

// Name is the marker's key in the plugin table.
func (*engineMarker) Name() string { return engineMarkerName }

// Initialize registers nothing: the marker is read, not run.
func (*engineMarker) Initialize(*gorm.DB) error { return nil }

// markEngine installs the engine marker on a handle openHandle opened.
func markEngine(session *gorm.DB, spec Spec, pool *sql.DB) error {
	marker := &engineMarker{dialect: spec.Dialect, pool: pool, boundLockWait: spec.BoundLockWait}
	if err := session.Use(marker); err != nil {
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
