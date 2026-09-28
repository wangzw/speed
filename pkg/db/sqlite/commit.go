package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	sqlitedriver "github.com/glebarez/sqlite"
)

// driverName is the database/sql driver every connection this engine opens
// goes through: the pure-Go driver, with its transactions ended cleanly when
// SQLite refuses a COMMIT.
const driverName = "speed-db-sqlite"

func init() { sql.Register(driverName, endingDriver{Driver: registeredDriver()}) }

// registeredDriver is the driver the SQLite binding registers under its own
// name. It is taken from the registration rather than built here, so the
// functions a host registers with the driver package reach these connections
// too.
func registeredDriver() driver.Driver {
	pool, err := sql.Open(sqlitedriver.DriverName, "")
	if err != nil {
		panic("db: the SQLite driver is not registered under " + sqlitedriver.DriverName + ": " + err.Error())
	}
	inner := pool.Driver()
	_ = pool.Close()
	return inner
}

// endingDriver opens connections through the SQLite driver and hands each one
// back with its transactions wrapped.
//
// SQLite keeps a transaction open when it refuses to commit it — a deferred
// foreign key that the COMMIT finds violated is the ordinary case — so that
// the application can repair it and commit again. database/sql does not know
// that: once Commit returns, the transaction is over for it and the connection
// goes back to the pool, still inside the transaction. Every later statement
// that lands on that connection then reports success and is never committed —
// another connection does not see it, and it is gone when the connection
// closes — and the next transaction begun on it fails outright. A transaction
// whose COMMIT was refused is therefore rolled back on its own connection
// before the connection is released, which is what the failed commit reported
// to the caller already says happened.
type endingDriver struct{ driver.Driver }

// Open opens one connection. A connection that cannot begin a transaction
// under a context is refused: it is the one form this wrapper begins them in.
func (d endingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	beginner, ok := conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.Join(fmt.Errorf("db: the SQLite driver's connection %T begins no transaction "+
			"under a context", conn), conn.Close())
	}
	return endingConn{Conn: conn, beginner: beginner}, nil
}

// endingConn is one SQLite connection whose transactions end on a refused
// COMMIT. Each interface database/sql looks for on a connection is answered
// by the connection underneath; one the driver does not implement is answered
// the way database/sql treats its absence.
type endingConn struct {
	driver.Conn
	beginner driver.ConnBeginTx
}

// The interfaces database/sql asserts on a connection, so a drift from them
// is caught where the type is written.
var (
	_ driver.ConnBeginTx        = endingConn{}
	_ driver.ConnPrepareContext = endingConn{}
	_ driver.ExecerContext      = endingConn{}
	_ driver.QueryerContext     = endingConn{}
	_ driver.Pinger             = endingConn{}
)

// Begin begins a transaction. database/sql calls BeginTx on this connection;
// Begin is here because driver.Conn requires it, and it takes the same path.
func (c endingConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx begins a transaction whose refused COMMIT rolls it back.
func (c endingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.beginner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return endingTx{Tx: tx}, nil
}

// PrepareContext prepares a statement.
func (c endingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, query)
	}
	return c.Prepare(query)
}

// ExecContext runs a statement that returns no rows.
func (c endingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if execer, ok := c.Conn.(driver.ExecerContext); ok {
		return execer.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// QueryContext runs a statement that returns rows.
func (c endingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.QueryerContext); ok {
		return queryer.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// Ping checks that the connection answers.
func (c endingConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

// endingTx is a transaction rolled back on its own connection when SQLite
// refuses to commit it.
type endingTx struct{ driver.Tx }

// Commit commits the transaction, and rolls it back when the COMMIT is
// refused. The refusal is what the caller is told; the rollback's own error is
// not, because SQLite answers it with "no transaction is active" whenever the
// refusal had ended the transaction already, and in every other case the
// caller's statement has failed either way.
func (t endingTx) Commit() error {
	err := t.Tx.Commit()
	if err != nil {
		_ = t.Rollback()
	}
	return err
}
