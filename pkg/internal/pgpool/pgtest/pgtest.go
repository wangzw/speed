// Package pgtest hands the tests a PostgreSQL server to run against, and gives
// every caller a database of its own on it.
//
// The server is the one the repository's compose stack brings up, reached at
// the address SPEED_TEST_PG_ADDR names and at loopback when it is unset; this
// package starts nothing. Everything it does
// happens over an ordinary SQL connection: creating a database per call,
// dropping it when the case ends, and clearing what a dead run left behind.
//
// Isolation is per database, not per server: Acquire creates a new one for
// each call and drops it when the test ends. The migration cases assert on
// which tables exist and on what the record table holds, and neither survives
// being shared with the packages go test runs in parallel or with the second
// test leg make check runs. DMLRole adds, on such a database, a role that may
// read and write rows and create nothing, for the cases about a service that
// runs without the privilege to change the schema.
//
// A machine that cannot reach the server fails these cases rather than skipping
// them: a gate that is silently skipped wherever it is actually meant to run is
// not a gate. The failure names the entry point that brings the stack up.
//
// Every module whose tests need PostgreSQL uses this one — the database module
// and the postgres backends of the key-value and event modules — so it lives
// with the pool wrapper they share, in pkg/internal/pgpool/pgtest, inside the
// pkg module's internal/ tree: it is read by tests only, belongs to no public
// surface, and the compiler holds that, keeping anything outside the module from
// importing it. See docs/adr/adr-shared-backend-unit-2026-09-21.md.
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
	_ "github.com/jackc/pgx/v5/stdlib" // the database/sql driver the statements below go over
)

const (
	// defaultAddr is where the compose stack publishes PostgreSQL by default.
	// A deployment that runs the stack elsewhere -- another compose project, or
	// the same one with other published ports -- points the tests at its
	// address with SPEED_TEST_PG_ADDR; an empty variable means this one.
	//
	// The variable moves the address, not the isolation. The housekeeping tells
	// a dead run's databases and roles from live ones by asking whether the
	// process id embedded in their names is alive in this machine's pid
	// namespace. Two copies of the repository running in parallel on the same
	// machine therefore leave each other's databases alone: each keeps the ones
	// whose pid is alive. Two hosts or two containers sharing one PostgreSQL
	// server are not covered -- a pid alive on one host means nothing on
	// another, and two hosts can produce the same pid and therefore the same
	// database name.
	defaultAddr = "127.0.0.1:5432"
	// superuser and password are the stack's own credentials, and they are
	// what creates and drops databases: both need the privilege to change the
	// server rather than the databases a service uses. They guard a throwaway
	// server bound to the loopback interface and are not secrets.
	superuser = "speed"
	password  = "speed"
	// maintenanceDatabase is the database the create and drop statements are
	// issued from. Neither can run from inside the database it acts on.
	maintenanceDatabase = "speed"
	// namePrefix starts the name of every database and role this package
	// creates. It is also how the housekeeping tells them from anything else
	// on a server someone else's tooling may share.
	namePrefix = "pgtest_p"
	// databaseInfix separates the owning process id from the counter in a
	// database's name.
	databaseInfix = "_n"
	// statementTimeout bounds one maintenance statement.
	statementTimeout = 30 * time.Second
	// readyTimeout bounds the wait for the server to answer, and readyInterval
	// is how often the wait looks while it does. The stack is normally already
	// healthy by the time a test binary starts -- `make deps-up` waits for its
	// healthchecks -- so this window is for the run that follows a bare
	// `docker compose up -d`, where the container is up but the server behind
	// it is still initialising.
	readyTimeout  = 30 * time.Second
	readyInterval = 150 * time.Millisecond
)

// address is where the tests reach PostgreSQL: defaultAddr unless
// SPEED_TEST_PG_ADDR names another address.
//
// It is read on each call rather than once at start-up, so a case that sets the
// variable observes the address it set.
func address() string { return envOr("SPEED_TEST_PG_ADDR", defaultAddr) }

// envOr returns the value of the named environment variable, or fallback when
// it is unset or empty.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// databaseCounter numbers the databases this process creates, so two calls in
// the same test binary cannot collide on one name.
var databaseCounter atomic.Int64

// Acquire hands back a locator for an empty PostgreSQL database of this test's
// own and drops the database when the test ends.
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

// dsn assembles the connection locator for a database on the shared server.
func dsn(database string) string {
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		superuser, password, address(), database)
}

// ready says a test binary has already found the server answering, and is
// never cleared: readiness is a property of the stack, and re-deciding it on
// every Acquire would put a round trip in front of each of the ninety-odd calls
// a suite makes.
//
// A failure is deliberately not remembered. A server that is merely still
// starting answers a moment later, so a case whose first Acquire raced it would
// otherwise fail every later case in the binary with a stale error.
var ready atomic.Bool

// ensureReady waits, once per test binary, for the server to answer, and then
// clears what dead runs left behind.
//
// The housekeeping rides on the same guard because it has the same shape: it is
// about what earlier runs left behind, it needs the server up, and nothing this
// binary creates can be stale until it has exited.
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

// waitForServer pings the maintenance database until it answers, the ping fails
// for a reason the wait cannot clear, or the bound is up.
func waitForServer() error {
	return waitForServerUntil(time.Now().Add(readyTimeout), readyInterval, ping)
}

// waitForServerUntil runs attempt until it succeeds, returns an error that is
// not about the connection, or reaches the deadline.
//
// A connection-level failure is what a server that is still starting answers
// with — the published port is not listening yet, it takes the connection and
// closes it, or the server refuses the connection while it initialises — so it
// is retried. Anything else is a server that answered but is not going to start
// answering differently, and it is reported at once. attempt and interval are
// parameters so the loop can be exercised without a server.
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

// cannotConnectNow is PostgreSQL's SQLSTATE for a server that is not accepting
// connections yet, which it answers with while it is starting up. It is not a
// kernel refusal, but it reaches the client the same way to the wait: the
// connection will be accepted once the server finishes initialising.
const cannotConnectNow = "57P03"

// retryableConnectionError reports whether err is a failure of the connection
// itself, the kind a server that is still coming up produces before it serves.
//
// It is an allowlist of the kernel's connection-level refusals and timeouts,
// plus the one server reply that says the server is not ready to take
// connections yet. It is not "any error from a ping": a malformed address, an
// unresolvable host, a wrong password and a database that does not exist all
// report through the same call, and none becomes ready by waiting.
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

// ping opens a connection to the maintenance database and lets it go.
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

// clearWhatDeadRunsLeft removes the databases and roles of processes that are
// gone. A stack outlives the runs that borrow it, so it accumulates them.
func clearWhatDeadRunsLeft() {
	dropStaleDatabases()
	dropStaleRoles()
}

// createDatabase creates one database on the shared server.
//
// The name is an identifier and cannot be a bound parameter, so it goes through
// pgx's identifier quoting. It is generated by this package and never comes
// from a test, but quoting it is what keeps that true of any later caller too.
func createDatabase(name string) error {
	return maintenanceStatement("CREATE DATABASE " + pgx.Identifier{name}.Sanitize())
}

// dropDatabase removes a database and whatever is still connected to it.
//
// FORCE disconnects what the test left behind. Without it a pool that outlived
// the test keeps the database alive, and the next run finds it populated.
func dropDatabase(name string) error {
	return maintenanceStatement(
		"DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)")
}

// maintenanceStatement runs one statement on the maintenance database.
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

// maintenance opens a pool on the maintenance database.
func maintenance() (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn(maintenanceDatabase))
	if err != nil {
		return nil, fmt.Errorf("opening the %s database at %s: %w", maintenanceDatabase, address(), err)
	}
	return pool, nil
}

// dropStaleDatabases removes the databases of processes that are gone.
//
// A reused server outlives the runs that borrowed it, and a run killed
// part-way leaves its databases behind. The owning process id is in the name,
// so a dead owner is something this can tell; a live one is left strictly
// alone, because another test binary is using it right now.
//
// Failures here are not reported: this is housekeeping, and a database that
// could not be dropped costs disk rather than correctness.
func dropStaleDatabases() {
	for _, name := range stale(`SELECT datname FROM pg_database WHERE datname LIKE $1`, databaseInfix) {
		_ = dropDatabase(name)
	}
}

// dropStaleRoles removes the roles DMLRole made for processes that are gone.
//
// A role belongs to the server rather than to a database, so dropping a dead
// run's database leaves its role behind. It runs after dropStaleDatabases: the
// grants a role held went with its database, and a role holding none can be
// dropped. Failures are not reported, for the same reason as there.
func dropStaleRoles() {
	for _, name := range stale(`SELECT rolname FROM pg_roles WHERE rolname LIKE $1`, roleInfix) {
		_ = maintenanceStatement("DROP ROLE IF EXISTS " + pgx.Identifier{name}.Sanitize())
	}
}

// stale lists the names this package created for processes that have since
// exited, out of what query returns for this package's name prefix; infix is
// what follows the process id in those names.
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

// ownerPID reads the process id out of a name this package created, in which
// infix follows the id.
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

// processAlive reports whether a process id is still in use. Signal 0 performs
// the permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// EPERM means the process exists and belongs to someone else, which is
	// still alive for this purpose.
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
