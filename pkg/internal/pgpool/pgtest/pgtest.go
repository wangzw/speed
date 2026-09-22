// Package pgtest hands the tests a PostgreSQL server to run against, and gives
// every caller a database of its own on it.
//
// It drives the docker CLI through os/exec rather than taking a container
// library as a dependency: starting a container and reading its published port
// is the whole of what it needs from docker, and a test-only dependency still
// lands in this module's dependency list. Everything after the container is up
// — creating a database, waiting for the server, dropping what a dead run left
// behind — goes over an ordinary SQL connection, so the only arguments that
// reach a subprocess are the constants below.
//
// One container serves the whole machine, under a fixed name, and is left
// running between test runs. That is deliberate. The alternative — a container
// per test binary, stopped at the end — needs a TestMain in every package that
// touches this one and leaks a container whenever a run is interrupted, while a
// single named container is reused by every run, costs one cold start in a
// day's work, and is removed by hand with a single docker rm.
//
// Isolation is per database, not per container: Acquire creates a new one for
// each call and drops it when the test ends. The migration cases assert on
// which tables exist and on what the record table holds, and neither survives
// being shared with the packages go test runs in parallel or with the second
// test leg make check runs.
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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // the database/sql driver the statements below go over
)

const (
	// containerName is fixed so that every test binary on this machine
	// reuses one server rather than starting its own.
	containerName = "speed-pgtest"
	// image is pinned: a migration case asserts on what this engine does
	// with DDL inside a transaction, and that is a property of a version.
	image = "postgres:17-alpine"
	// superuser and password are the container's own credentials. They guard
	// a throwaway server bound to the loopback interface and are not
	// secrets; writing them here keeps them out of the environment, where a
	// diagnostic dump would pick them up.
	superuser = "postgres"
	password  = "pgtest"
	// maintenanceDatabase is the database the create and drop statements are
	// issued from. Neither can run from inside the database it acts on.
	maintenanceDatabase = "postgres"
	// runningState is what the running flag renders as, and it is named
	// rather than written twice inline.
	//
	// Unlike the words in recovery_test.go it is not docker's prose and
	// cannot move with a docker version: the field behind it is a boolean,
	// and a template renders one through the standard library, so the only
	// two things it can ever print are this and its opposite. What can move
	// is the field going away or ceasing to be a boolean, and that does not
	// arrive as a word this fails to match — the template itself fails, the
	// call exits non-zero, and the look reports it. No case holds this value
	// to a real docker for that reason.
	runningState = "true"
	// namePrefix starts the name of every database this package creates. It
	// is also how the housekeeping tells them from anything else on a
	// container someone else's tooling may share.
	namePrefix = "pgtest_p"
	// readyTimeout bounds the wait for a cold container to accept
	// connections, the image pull included.
	readyTimeout = 3 * time.Minute
	// dockerTimeout bounds one docker invocation.
	dockerTimeout = 2 * time.Minute
	// statementTimeout bounds one maintenance statement.
	statementTimeout = 30 * time.Second
)

// The pace of the wait that follows a create lost to another test binary: how
// long it lasts, and how often it looks while it does.
//
// The bound is generous against what the race looks like — the window in which
// a losing creator cannot yet see the winner's container measured 100–200ms on
// a cold image locally, and a loaded CI machine is slower and wider than that —
// while staying bounded, so that a daemon that is genuinely broken ends in a
// failure carrying what docker said rather than in a hang.
//
// These are the production values, and the production path is their only
// reader. A case drives the same code with a pace of its own, through the
// parameters of ensureContainerWithin: reaching the give-up path cannot cost
// the production bound, and a call a case has given up on must not be left
// reading package state that a later case can change underneath it.
const (
	recoveryBound    = 30 * time.Second
	recoveryInterval = 150 * time.Millisecond
)

// databaseCounter numbers the databases this process creates, so two calls in
// the same test binary cannot collide on one name.
var databaseCounter atomic.Int64

// Skip reports why this machine cannot run a PostgreSQL case, or an empty
// string when it can.
//
// A machine without docker skips, which is what lets the suite run on a laptop
// that has none. Callers turn that into a failure under CI: a gate that is
// silently skipped wherever it is actually meant to run is not a gate.
func Skip() string {
	if _, err := exec.LookPath("docker"); err != nil {
		return "docker is not on PATH, so the PostgreSQL cases cannot run"
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	if _, err := docker(exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")); err != nil {
		return fmt.Sprintf("the docker daemon is not reachable, so the PostgreSQL cases cannot run: %v", err)
	}
	return ""
}

// requireDocker skips the calling test when this machine cannot reach docker,
// and fails it instead where CI is set.
//
// The decision is one policy — a laptop without docker skips, a run that is
// meant to exercise these cases does not get to — and it is here rather than at
// each caller because a gate that is silently skipped wherever it is actually
// meant to run is not a gate, and that is exactly what a copy left behind on an
// edit becomes.
func requireDocker(t *testing.T) {
	t.Helper()
	if reason := Skip(); reason != "" {
		if _, underCI := os.LookupEnv("CI"); underCI {
			t.Fatalf("CI is set, so a skip here would leave this gate unguarded: %s", reason)
		}
		t.Skip(reason)
	}
}

// Acquire hands back a locator for an empty PostgreSQL database of this test's
// own, starting the shared container if it is not already up, and drops the
// database when the test ends.
//
// It skips the test when this machine has no docker, unless CI is set in the
// environment, where it fails the test instead.
func Acquire(t *testing.T) string {
	t.Helper()
	requireDocker(t)
	port, err := server()
	if err != nil {
		t.Fatalf("bringing up the %s container: %v", containerName, err)
	}

	name := fmt.Sprintf("%s%d_n%d", namePrefix, os.Getpid(), databaseCounter.Add(1))
	if err := createDatabase(port, name); err != nil {
		t.Fatalf("creating the database %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(port, name); err != nil {
			t.Errorf("dropping the database %s: %v", name, err)
		}
	})
	return DSN(port, name)
}

// DSN assembles the connection locator for a database on the shared container.
func DSN(port, database string) string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%s/%s?sslmode=disable",
		superuser, password, port, database)
}

// The result of bringing the container up is held so the work happens once per
// test binary however many databases it acquires — the successful result, that
// is. A failure is not remembered: docker may be reachable again a moment
// later, and a case whose first Acquire raced a restarting daemon would
// otherwise fail every later case with a stale error.
var (
	serverMu   sync.Mutex
	serverPort string
	serverUp   bool
)

// server reports the host port the shared container's PostgreSQL is published
// on, starting or reusing the container as needed.
func server() (string, error) {
	serverMu.Lock()
	defer serverMu.Unlock()
	if serverUp {
		return serverPort, nil
	}
	port, err := startServer()
	if err != nil {
		return "", err
	}
	serverPort, serverUp = port, true
	return port, nil
}

// startServer leaves the shared container running and reports its published
// port.
func startServer() (string, error) {
	if err := ensureContainer(); err != nil {
		return "", err
	}
	port, err := publishedPort()
	if err != nil {
		return "", err
	}
	if err := waitReady(port); err != nil {
		return "", err
	}
	dropStaleDatabases(port)
	return port, nil
}

// ensureContainer leaves the shared container running, whichever of the three
// states it was in.
//
// The create path tolerates losing the race with another test binary starting
// at the same moment: docker refuses the duplicate name, and the container that
// won is the one both processes go on to use.
//
// Losing that race does not end with the refusal. docker takes the name before
// the container can be looked at, so the process that lost spends a window
// being told there is no such object — measured at 100–200ms on a cold image,
// and wider on a machine under load — and only then finds the winner's
// container. The wait below is that window, and the container it ends on is the
// winner's: a losing process never creates one of its own.
func ensureContainer() error { return ensureContainerWithin(recoveryBound, recoveryInterval) }

// ensureContainerWithin is ensureContainer with the pace of that wait taken as
// parameters, covering the path entire: the look before the create, the refused
// create, and the wait that follows it.
//
// The pace is a parameter and not package state a case can write, because a
// case that gives up on a call has to be able to walk away from it: a call
// still running reads the pace on every round it makes, and what it reads must
// not be something a later case changes underneath it. The seam covers the
// whole path for the same reason the cases assert on call sequences — what a
// case drives has to be the sequence from the first look to the container
// coming up, and a seam around the wait alone would leave the create outside
// it, along with the container that is there and stopped, which never reaches
// the wait at all.
func ensureContainerWithin(bound, interval time.Duration) error {
	if state, err := inspectRunning(); err == nil {
		if state == runningState {
			return nil
		}
		// The container is there and not running: whoever created it did not
		// start it, or died between the two. This is not the race — the object
		// is there to be looked at, so this process is in nobody's window —
		// and a start that fails here is reported now rather than waited on.
		// Retrying it would hold a stopped container that will not come back,
		// which is a lasting failure, for as long as the wait lasts, and every
		// test binary on the machine goes through here.
		if err := startContainer(); err != nil {
			return fmt.Errorf("starting the existing %s container: %w", containerName, err)
		}
		return nil
	}
	createErr := createContainer()
	if createErr == nil {
		return nil
	}
	return awaitContainerWithin(bound, interval, createErr)
}

// awaitContainerWithin waits for the container another process is creating to
// appear and run, looking every interval until the bound is up, and reports
// what docker said along the way when it never does.
//
// Only inspect and start are repeated. A create is not tried again on purpose:
// the name on this machine belongs to whoever holds it, so a losing process
// gains nothing by asking for it twice, and the container it has to end up on
// is the one that won. A create refused for any other reason — a daemon that is
// down, an image that cannot be pulled — ends the same way the window does:
// within the bound, carrying docker's last word.
func awaitContainerWithin(bound, interval time.Duration, createErr error) error {
	deadline := time.Now().Add(bound)
	var (
		state      string
		inspectErr error
		startErr   error
	)
	for {
		state, inspectErr = inspectRunning()
		switch {
		case inspectErr == nil && state == runningState:
			return nil
		case inspectErr == nil:
			// The container is there and not running yet: the winner is
			// between creating it and starting it, or died in between. A start
			// refused in the window is as transient as the missing object is,
			// and the round after it decides rather than this one.
			startErr = startContainer()
		}
		if time.Now().After(deadline) {
			return recoveryFailure(bound, createErr, startErr, state, inspectErr)
		}
		time.Sleep(interval)
	}
}

// recoveryFailure is a create that was never made good: the refusal itself, and
// the last word from each call the wait made after it.
//
// All of it is carried because of what this race cost while it was undiagnosed.
// The failures CI saw said only that the container had not been created; the
// look that failed last, which is the one thing that says where the wait
// stopped, was made and then thrown away by the recovery that made it.
func recoveryFailure(bound time.Duration, createErr, startErr error, state string, inspectErr error) error {
	failure := fmt.Errorf("creating the %s container from %s: %w", containerName, image, createErr)
	if inspectErr != nil {
		failure = fmt.Errorf("%w; after %s the container was still not there: %w", failure, bound, inspectErr)
	} else {
		failure = fmt.Errorf("%w; after %s the container was still not running, and the last look at it read %q",
			failure, bound, state)
	}
	if startErr != nil {
		failure = fmt.Errorf("%w; and the last start of it failed: %w", failure, startErr)
	}
	return failure
}

// inspectRunning reports whether the shared container is running, and fails
// when there is no such object.
func inspectRunning() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	out, err := docker(exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{.State.Running}}", containerName))
	return strings.TrimSpace(out), err
}

// startContainer starts the existing container again.
func startContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	_, err := docker(exec.CommandContext(ctx, "docker", "start", containerName))
	return err
}

// createContainer creates and starts the shared container.
//
// The host port is ephemeral: several servers on one machine must not fight
// over 5432, and nothing here needs a fixed one.
func createContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	_, err := docker(exec.CommandContext(ctx, "docker", "run", "--detach",
		"--name", containerName,
		"--env", "POSTGRES_PASSWORD="+password,
		"--env", "POSTGRES_USER="+superuser,
		"--publish", "127.0.0.1::5432",
		image))
	return err
}

// loopbackPortFormat renders the host port 5432 is published on at 127.0.0.1,
// and nothing when it is published nowhere else or nowhere at all.
//
// The binding is read as a field rather than out of prose: `docker port` prints
// address:port per family, and the address side of an IPv6 line carries colons
// of its own, so reading it back means splitting an address off again. The two
// other things this package asks docker are already templated, and this one
// answers with the value itself.
//
// The address is held to the ones DSN can dial. A container this package
// created publishes on 127.0.0.1, but ensureContainer reuses whatever
// container holds the name, whoever made it: an earlier fixture of this
// repository published on every interface — which loopback reaches too — and
// one published on some other single address, or per family with a port each,
// then reads as no port that can be dialled rather than as a port nothing
// listens on — the failure this whole path exists to keep from arriving at the
// connection instead of at the read.
// Each match ends in a newline. A container may hold more than one binding on
// the address, and a range that wrote them end to end would render two ports as
// one number that is neither.
const loopbackPortFormat = `{{range (index .NetworkSettings.Ports "5432/tcp")}}` +
	`{{if or (eq .HostIp "127.0.0.1") (eq .HostIp "0.0.0.0") (eq .HostIp "")}}{{.HostPort}}{{"\n"}}{{end}}{{end}}`

// publishedPort reports the host port 5432 inside the container is mapped to.
func publishedPort() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	out, err := docker(exec.CommandContext(ctx, "docker", "inspect",
		"--format", loopbackPortFormat, containerName))
	if err != nil {
		return "", fmt.Errorf("reading the published port of %s: %w", containerName, err)
	}
	port, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if port == "" {
		// An empty answer is two situations that read alike and are put
		// right differently, so the look that tells them apart is made
		// here rather than left to whoever reads the failure: a container
		// that is no longer running has no bindings at all, while one
		// published somewhere else has bindings and none on this address.
		if state, err := inspectRunning(); err == nil && state != runningState {
			return "", fmt.Errorf("%s is not running, so it publishes nothing: it was up when this "+
				"look began and was stopped or removed in between", containerName)
		}
		return "", fmt.Errorf("%s publishes 5432 on no loopback address, so nothing here can be "+
			"dialled: remove that container and let this package create its own", containerName)
	}
	// The port is held to the range one can be in. What comes back is a field
	// rather than prose, so this is not parsing docker's output over again; it
	// is refusing at the read a value that could only fail at the connection,
	// where the report would name PostgreSQL and not this.
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", fmt.Errorf("the loopback binding of %s reads as %q, which is no port: %w",
			containerName, port, err)
	}
	return port, nil
}

// docker runs a docker command and returns its standard output, carrying
// whatever it wrote to standard error into the error: docker's own message is
// the only thing that says why a command failed.
//
// Every caller passes a command built from the constants in this file, which is
// why the arguments are assembled at the call sites rather than forwarded
// through here — nothing a test writes reaches a subprocess.
func docker(cmd *exec.Cmd) (string, error) {
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// WaitDelay bounds the wait after the context kills the process, so a
	// docker CLI wedged on a pipe cannot hang the test binary.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	if message := strings.TrimSpace(stderr.String()); message != "" {
		return string(out), fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, message)
	}
	return string(out), fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
}

// waitReady blocks until the server accepts connections.
//
// The wait ends on a real connection over the published port rather than on the
// container's own health report: the entrypoint starts the server once on a
// local socket to run its initialisation scripts, and a client that connects in
// that window is dropped when it restarts.
func waitReady(port string) error {
	deadline := time.Now().Add(readyTimeout)
	var last error
	for time.Now().Before(deadline) {
		last = ping(port)
		if last == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("the %s container did not accept connections within %s: %w",
		containerName, readyTimeout, last)
}

// ping opens a connection to the maintenance database and lets it go.
func ping(port string) error {
	pool, err := maintenance(port)
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		return fmt.Errorf("reaching %s on port %s: %w", containerName, port, err)
	}
	return nil
}

// maintenance opens a pool on the maintenance database.
func maintenance(port string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", DSN(port, maintenanceDatabase))
	if err != nil {
		return nil, fmt.Errorf("opening the %s database on port %s: %w", maintenanceDatabase, port, err)
	}
	return pool, nil
}

// createDatabase creates one database on the shared container.
//
// The name is an identifier and cannot be a bound parameter, so it goes through
// pgx's identifier quoting. It is generated by this package and never comes
// from a test, but quoting it is what keeps that true of any later caller too.
func createDatabase(port, name string) error {
	return maintenanceStatement(port, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
}

// dropDatabase removes a database and whatever is still connected to it.
//
// FORCE disconnects what the test left behind. Without it a pool that outlived
// the test keeps the database alive, and the next run finds it populated.
func dropDatabase(port, name string) error {
	return maintenanceStatement(port,
		"DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
}

// maintenanceStatement runs one statement on the maintenance database.
func maintenanceStatement(port, statement string) error {
	pool, err := maintenance(port)
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()
	if _, err := pool.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("running a maintenance statement on %s: %w", containerName, err)
	}
	return nil
}

// dropStaleDatabases removes the databases of processes that are gone.
//
// A reused container outlives the runs that borrowed it, and a run killed
// part-way leaves its databases behind. The owning process id is in the name,
// so a dead owner is something this can tell; a live one is left strictly
// alone, because another test binary is using it right now.
//
// Failures here are not reported: this is housekeeping, and a database that
// could not be dropped costs disk rather than correctness.
func dropStaleDatabases(port string) {
	for _, name := range stale(port) {
		_ = dropDatabase(port, name)
	}
}

// stale lists the databases this package created for processes that have since
// exited.
func stale(port string) []string {
	pool, err := maintenance(port)
	if err != nil {
		return nil
	}
	defer func() { _ = pool.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), statementTimeout)
	defer cancel()

	rows, err := pool.QueryContext(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1`, namePrefix+"%")
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
		pid, ok := ownerPID(name)
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

// ownerPID reads the process id out of a database name this package created.
func ownerPID(name string) (int, bool) {
	rest, found := strings.CutPrefix(name, namePrefix)
	if !found {
		return 0, false
	}
	digits, _, found := strings.Cut(rest, "_n")
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
