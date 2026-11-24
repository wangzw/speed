package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	sqlitedriver "github.com/glebarez/sqlite"
)

// driverName is the database/sql driver every connection this engine opens goes
// through: the pure-Go driver, with its transactions begun and ended here.
const driverName = "speed-db-sqlite"

func init() { sql.Register(driverName, transactionDriver{Driver: registeredDriver()}) }

// registeredDriver is taken from the binding's own registration, so functions a
// host registers with the driver package reach these connections too.
func registeredDriver() driver.Driver {
	pool, err := sql.Open(sqlitedriver.DriverName, "")
	if err != nil {
		panic("db: the SQLite driver is not registered under " + sqlitedriver.DriverName + ": " + err.Error())
	}
	inner := pool.Driver()
	_ = pool.Close()
	return inner
}

// transactionDriver opens connections through the SQLite driver, beginning transactions in the locator's mode. SQLite keeps a transaction open when it refuses to commit it, while database/sql treats Commit as final and returns the connection still inside one; a refused COMMIT is therefore rolled back on its own connection before the connection is released.
type transactionDriver struct{ driver.Driver }

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

type transactionConn struct {
	driver.Conn
	execer  driver.ExecerContext
	locator sqliteLocator
}

var (
	_ driver.ConnBeginTx        = transactionConn{}
	_ driver.ConnPrepareContext = transactionConn{}
	_ driver.ExecerContext      = transactionConn{}
	_ driver.QueryerContext     = transactionConn{}
	_ driver.Pinger             = transactionConn{}
)

// Begin is required by driver.Conn; it takes the same path as BeginTx.
func (c transactionConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx begins a transaction with the statement beginStatement gives. A
// connection whose query_only is on refuses every BEGIN but a deferred one, so
// that refusal is retried as DEFERRED whatever mode the locator names.
func (c transactionConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	statement, err := beginStatement(c.locator, opts.ReadOnly)
	if err != nil {
		return nil, err
	}
	tx := transaction{execer: c.execer}
	if err := tx.run(statement); err != nil {
		if statement == deferredBegin || !c.queryOnly() {
			return nil, err
		}
		if err := tx.run(deferredBegin); err != nil {
			return nil, err
		}
	}
	return tx, nil
}

// queryOnly reports whether the connection's query_only is on. A connection
// that does not answer is taken for one whose query_only is off.
func (c transactionConn) queryOnly() bool {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return false
	}
	rows, err := queryer.QueryContext(context.Background(), "PRAGMA query_only", nil)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	value := make([]driver.Value, 1)
	if rows.Next(value) != nil {
		return false
	}
	on, ok := value[0].(int64)
	return ok && on != 0
}

func (c transactionConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return preparer.PrepareContext(ctx, query)
	}
	return c.Prepare(query)
}

func (c transactionConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.execer.ExecContext(ctx, query, args)
}

func (c transactionConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if queryer, ok := c.Conn.(driver.QueryerContext); ok {
		return queryer.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c transactionConn) Ping(ctx context.Context) error {
	if pinger, ok := c.Conn.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

type transaction struct{ execer driver.ExecerContext }

// Commit rolls the transaction back when the COMMIT is refused, and reports
// only the refusal: SQLite answers the rollback with "no transaction is active"
// whenever the refusal had already ended the transaction.
func (t transaction) Commit() error {
	err := t.run("COMMIT")
	if err != nil {
		_ = t.Rollback()
	}
	return err
}

func (t transaction) Rollback() error { return t.run("ROLLBACK") }

func (t transaction) run(statement string) error {
	_, err := t.execer.ExecContext(context.Background(), statement, nil)
	return err
}
