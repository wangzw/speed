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
// goes through: the pure-Go driver, with its transactions begun and ended by
// this subpackage.
const driverName = "speed-db-sqlite"

func init() { sql.Register(driverName, transactionDriver{Driver: registeredDriver()}) }

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

// transactionDriver opens connections through the SQLite driver and hands each
// one back with its transactions begun and ended here.
//
// A transaction begins in the mode beginStatement gives the locator —
// IMMEDIATE unless the locator names another — so that one reading before it
// writes waits for a concurrent writer within the busy timeout instead of
// being refused the write lock at once.
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
type transactionDriver struct{ driver.Driver }

// Open opens one connection. A locator whose _txlock the driver does not know
// is refused, and so is a connection that cannot run a statement under a
// context: it is the one form this wrapper begins and ends transactions in.
func (d transactionDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		return nil, errors.Join(fmt.Errorf("db: the SQLite driver's connection %T runs no statement "+
			"under a context", conn), conn.Close())
	}
	locator, _ := readLocator(name)
	if _, err := beginStatement(locator, false); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return transactionConn{Conn: conn, execer: execer, locator: locator}, nil
}

// transactionConn is one SQLite connection whose transactions begin in the
// locator's mode and end on a refused COMMIT. Each interface database/sql looks
// for on a connection is answered by the connection underneath; one the driver
// does not implement is answered the way database/sql treats its absence.
type transactionConn struct {
	driver.Conn
	execer  driver.ExecerContext
	locator sqliteLocator
}

// The interfaces database/sql asserts on a connection, so a drift from them
// is caught where the type is written.
var (
	_ driver.ConnBeginTx        = transactionConn{}
	_ driver.ConnPrepareContext = transactionConn{}
	_ driver.ExecerContext      = transactionConn{}
	_ driver.QueryerContext     = transactionConn{}
	_ driver.Pinger             = transactionConn{}
)

// Begin begins a transaction. database/sql calls BeginTx on this connection;
// Begin is here because driver.Conn requires it, and it takes the same path.
func (c transactionConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx begins a transaction with the statement beginStatement gives, run
// under no context as the driver runs its own; database/sql rolls the
// transaction back when ctx ends.
func (c transactionConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	statement, err := beginStatement(c.locator, opts.ReadOnly)
	if err != nil {
		return nil, err
	}
	tx := transaction{execer: c.execer}
	if err := tx.run(statement); err != nil {
		return nil, err
	}
	return tx, nil
}

// PrepareContext prepares a statement.
func (c transactionConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, query)
	}
	return c.Prepare(query)
}

// ExecContext runs a statement that returns no rows.
func (c transactionConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.execer.ExecContext(ctx, query, args)
}

// QueryContext runs a statement that returns rows.
func (c transactionConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.QueryerContext); ok {
		return queryer.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

// Ping checks that the connection answers.
func (c transactionConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

// transaction is a transaction transactionConn began, ended by its own
// statements on the connection it runs on.
type transaction struct{ execer driver.ExecerContext }

// Commit commits the transaction, and rolls it back when the COMMIT is
// refused. The refusal is what the caller is told; the rollback's own error is
// not, because SQLite answers it with "no transaction is active" whenever the
// refusal had ended the transaction already, and in every other case the
// caller's statement has failed either way.
func (t transaction) Commit() error {
	err := t.run("COMMIT")
	if err != nil {
		_ = t.Rollback()
	}
	return err
}

// Rollback rolls the transaction back.
func (t transaction) Rollback() error { return t.run("ROLLBACK") }

// run runs one statement of the transaction's own.
func (t transaction) run(statement string) error {
	_, err := t.execer.ExecContext(context.Background(), statement, nil)
	return err
}
