package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Connection runs fn on one connection of the handle's pool, taken within
// wait, and holds every wait of fn's statements for a lock another connection
// holds to what remained of wait once the connection was taken.
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
// The session fn receives is pinned to the connection, and so is everything
// derived from it, a transaction included. fn has to be done with it when it
// returns — every result set closed, every transaction ended — because the
// connection then goes back to the pool with the lock-wait setting it had
// before; a connection whose setting cannot be put back is closed instead of
// being handed on with the bound. fn's own error is returned as it is.
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
	settle, done := setting()
	restore, err := engine.boundLockWait(settle, conn, max(time.Until(deadline), time.Millisecond))
	done()
	if err != nil {
		dropFromPool(conn)
		return fmt.Errorf("db: bounding how long a connection of the %s pool waits for a lock: %w",
			engine.dialect, err)
	}
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
	session.Statement.ConnPool = conn
	return fn(session)
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
