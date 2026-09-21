// Package pgtest hands the tests a PostgreSQL server to run against, and gives
// every caller a database of its own on it.
//
// It drives the docker CLI through os/exec rather than taking a container
// library as a dependency, for the same reason pkg/db's own fixture does: a
// test-only dependency still lands in this module's dependency list.
//
// Isolation is per database rather than per container. Several callers run at
// once — go test runs the packages of a module in parallel and make check runs
// two legs — and the cases assert on what a table holds, which does not survive
// being shared.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	// containerName is fixed so that every test binary on this machine reuses
	// one server rather than starting its own.
	containerName = "speed-pgtest"
	// image is pinned: the cases lean on ON CONFLICT with a partial WHERE and
	// on trim_scale, and both are properties of a version.
	image = "postgres:17-alpine"
	// The container's own credentials. They guard a throwaway server bound to
	// the loopback interface; writing them here keeps them out of the
	// environment, where a diagnostic dump would pick them up.
	superuser = "postgres"
	password  = "pgtest"
	// maintenanceDatabase is the database the create and drop statements are
	// issued from: neither can run from inside the database it acts on.
	maintenanceDatabase = "postgres"
	runningState        = "true"
	// namePrefix starts the name of every database this package creates, so the
	// housekeeping can tell them from anything else on a shared container.
	namePrefix = "eventpgtest_"
	// readyTimeout bounds the wait for a cold container to accept connections,
	// the image pull included.
	readyTimeout = 3 * time.Minute
	// dockerTimeout bounds one docker invocation.
	dockerTimeout = 2 * time.Minute
)

// serial keeps the generated database names unique within one process.
var serial atomic.Int32

// Acquire returns a connection string for a database that no other caller is
// using and that holds nothing. The database is dropped when the test ends.
func Acquire(t *testing.T) string {
	t.Helper()
	requireDocker(t)
	addr, port := ensureServer(t)
	name := fmt.Sprintf("%s%d_%d", namePrefix, os.Getpid(), serial.Add(1))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	maintenance := dsn(addr, port, maintenanceDatabase)
	conn, err := pgx.Connect(ctx, maintenance)
	if err != nil {
		t.Fatalf("pgtest: connecting to the server: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("pgtest: creating database %s: %v", name, err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		conn, err := pgx.Connect(dropCtx, maintenance)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(dropCtx) }()
		_, _ = conn.Exec(dropCtx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})
	return dsn(addr, port, name)
}

// dsn assembles a connection string for one database.
func dsn(addr string, port int, database string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", superuser, password, addr, port, database)
}

// requireDocker fails the case when there is no docker to drive.
//
// Under CI the failure is unconditional: a case that silently skips where it is
// meant to run is not a gate.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("pgtest: docker is required in CI and was not found: %v", err)
		}
		t.Skip("pgtest: docker is not installed, and the cases that need a real PostgreSQL are skipped")
	}
}

// ensureServer returns the address and port of the shared server, starting it
// when it is not already running.
func ensureServer(t *testing.T) (addr string, port int) {
	t.Helper()
	if !running(t) {
		if exists(t) {
			// A container left over from an interrupted run is removed rather
			// than reused: nothing here can tell a half-started one from a good
			// one.
			run(t, "rm", "-f", containerName)
		}
		run(t, "run", "-d", "--name", containerName,
			"-e", "POSTGRES_PASSWORD="+password, "-p", "0:5432", image)
	}
	addr, port = publishedAddr(t)
	waitReady(t, addr, port)
	return addr, port
}

func running(t *testing.T) bool {
	t.Helper()
	out, err := docker(t, "inspect", "-f", "{{.State.Running}}", containerName)
	return err == nil && strings.TrimSpace(out) == runningState
}

func exists(t *testing.T) bool {
	t.Helper()
	_, err := docker(t, "inspect", "-f", "{{.Name}}", containerName)
	return err == nil
}

// publishedAddr reads the host address and port docker mapped the server's own
// port onto.
func publishedAddr(t *testing.T) (string, int) {
	t.Helper()
	out, err := docker(t, "port", containerName, "5432")
	if err != nil {
		t.Fatalf("pgtest: reading the published port: %v", err)
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	i := strings.LastIndex(line, ":")
	if i < 0 {
		t.Fatalf("pgtest: docker port printed %q, which names no port", out)
	}
	port, err := strconv.Atoi(line[i+1:])
	if err != nil {
		t.Fatalf("pgtest: docker port printed %q, whose port is not a number", out)
	}
	host := "127.0.0.1"
	return host, port
}

// waitReady polls the server until it accepts connections, so a case never
// races the container's own startup.
func waitReady(t *testing.T, addr string, port int) {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := pgx.Connect(ctx, dsn(addr, port, maintenanceDatabase))
		if err == nil {
			_ = conn.Close(ctx)
			cancel()
			return
		}
		last = err
		cancel()
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("pgtest: the server did not accept connections within %s: %v", readyTimeout, last)
}

func docker(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	//nolint:gosec // every argument is one of this file's own constants or a database name built from them; nothing a case supplies reaches the command line
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
	}
	return string(out), err
}

func run(t *testing.T, args ...string) {
	t.Helper()
	out, err := docker(t, args...)
	if err != nil {
		t.Fatalf("pgtest: docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
