// Package pgtest hands a test the shared PostgreSQL server and a database of its own on it.
package pgtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultAddr         = "127.0.0.1:5432"
	superuser           = "speed"
	password            = "speed"
	maintenanceDatabase = "speed"
	namePrefix          = "pgtest_p"
	databaseInfix       = "_n"
	statementTimeout    = 30 * time.Second
	readyTimeout        = 30 * time.Second
	readyInterval       = 150 * time.Millisecond
)

func address() string { return envOr("SPEED_TEST_PG_ADDR", defaultAddr) }

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

var databaseCounter atomic.Int64

// Acquire hands back a locator for an empty PostgreSQL database of this test's own and drops the
// database when the test ends.
func Acquire(t *testing.T) string {
	t.Helper()
	ensureReady(t)

	name := fmt.Sprintf("%s%d%s%d", namePrefix, os.Getpid(), databaseInfix, databaseCounter.Add(1))
	if err := createDatabase(name); err != nil {
		t.Fatalf("pgtest: creating the database %s on the PostgreSQL test server at %s: %v", name, address(), err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(name); err != nil {
			t.Errorf("dropping the database %s: %v", name, err)
		}
	})
	return dsn(name)
}

func dsn(database string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		superuser, password, address(), database)
}

var ready atomic.Bool

func ensureReady(t *testing.T) {
	t.Helper()
	if ready.Load() {
		return
	}
	if err := waitForServer(); err != nil {
		t.Fatalf("pgtest: the PostgreSQL test server at %s did not answer (the wait is bounded at %s): %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", address(), readyTimeout, err)
	}
	if ready.CompareAndSwap(false, true) {
		clearWhatDeadRunsLeft()
	}
}

func waitForServer() error {
	return waitForServerUntil(time.Now().Add(readyTimeout), readyInterval, ping)
}

func waitForServerUntil(deadline time.Time, interval time.Duration, attempt func() error) error {
	for {
		err := attempt()
		if err == nil {
			return nil
		}
		if !retryableConnectionError(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(interval)
	}
}

const cannotConnectNow = "57P03"

func retryableConnectionError(err error) bool {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE),
		errors.Is(err, syscall.ETIMEDOUT):
		return true
	case errors.Is(err, os.ErrDeadlineExceeded):
		return true
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return true
	}
	var refusal *pgconn.PgError
	return errors.As(err, &refusal) && refusal.Code == cannotConnectNow
}

func ping() error {
	pool, err := maintenance()
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()
	return pool.PingContext(ctx)
}

func clearWhatDeadRunsLeft() {
	dropStaleDatabases()
	dropStaleRoles()
}

func createDatabase(name string) error {
	return maintenanceStatement("CREATE DATABASE " + pgx.Identifier{name}.Sanitize())
}

func dropDatabase(name string) error {
	return maintenanceStatement(
		"DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)")
}

func maintenanceStatement(statement string) error {
	pool, err := maintenance()
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()
	if _, err := pool.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("running a maintenance statement on %s: %w", address(), err)
	}
	return nil
}

func maintenance() (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn(maintenanceDatabase))
	if err != nil {
		return nil, fmt.Errorf("opening the %s database at %s: %w", maintenanceDatabase, address(), err)
	}
	return pool, nil
}

func dropStaleDatabases() {
	for _, name := range stale(`SELECT datname FROM pg_database WHERE datname LIKE $1`, databaseInfix) {
		_ = dropDatabase(name)
	}
}

func dropStaleRoles() {
	for _, name := range stale(`SELECT rolname FROM pg_roles WHERE rolname LIKE $1`, roleInfix) {
		_ = maintenanceStatement("DROP ROLE IF EXISTS " + pgx.Identifier{name}.Sanitize())
	}
}

func stale(query, infix string) []string {
	pool, err := maintenance()
	if err != nil {
		return nil
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()

	rows, err := pool.QueryContext(ctx, query, namePrefix+"%")
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	self := os.Getpid()
	var leftovers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil
		}
		pid, ok := ownerPID(name, infix)
		if !ok || pid == self || processAlive(pid) {
			continue
		}
		leftovers = append(leftovers, name)
	}
	if rows.Err() != nil {
		return nil
	}
	return leftovers
}

func ownerPID(name, infix string) (int, bool) {
	rest, found := strings.CutPrefix(name, namePrefix)
	if !found {
		return 0, false
	}
	digits, _, found := strings.Cut(rest, infix)
	if !found {
		return 0, false
	}
	var pid int
	if _, err := fmt.Sscanf(digits, "%d", &pid); err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
