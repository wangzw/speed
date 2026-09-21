// Package redistest hands the tests a Redis server to run against, and gives
// every caller a database of its own on it.
//
// It drives the docker CLI through os/exec rather than taking a container
// library as a dependency: starting a container and reading its published port
// is the whole of what it needs from docker, and a test-only dependency still
// lands in a module's dependency list. The only values that reach a subprocess
// are the constants below.
//
// One container serves the whole machine under a fixed name and is left running
// between test runs, so a day's work costs one cold start rather than one per
// binary. Isolation is per database index, not per container: Acquire hands each
// caller one of the sixteen a default Redis has, and clears it first, because
// go test runs the packages of one module in parallel and make check runs two
// legs.
//
// Two modules use this — the key-value one and the event one — which is why it
// lives beside the client wrapper they share rather than inside either. The
// cost is that it is exported rather than internal, so it is part of that
// package's surface: see docs/adr/adr-shared-backend-unit-2026-09-21.md.
package redistest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// containerName is fixed so that every test binary on this machine reuses
	// one server rather than starting its own.
	containerName = "speed-redistest"
	// image is pinned: the two implementations lean on stream consumer groups
	// and on SET's KEEPTTL, and both are properties of a version.
	image = "redis:7-alpine"
	// databases is how many logical databases a default Redis offers. They are
	// the unit of isolation here; a key prefix would not do, because some cases
	// assert on which keys exist.
	databases = 16
	// readyTimeout bounds the wait for a cold container to answer, the image
	// pull included.
	readyTimeout = 3 * time.Minute
	// dockerTimeout bounds one docker invocation.
	dockerTimeout = 2 * time.Minute
	// runningState is what docker's template prints for a running container.
	runningState = "true"
	// bringUpBound bounds the wait that follows a create lost to another test
	// binary, and bringUpInterval is how often it looks while it waits.
	bringUpBound    = 30 * time.Second
	bringUpInterval = 150 * time.Millisecond
)

// nextDB hands out database indices. A counter rather than a random pick keeps
// the sequence reproducible, and it is seeded from the process id so that two
// test binaries running at once — go test across packages, or make check's two
// legs — walk different stretches of the sixteen a default Redis has: with a
// plain counter both would start at zero, and the second binary's FlushDB
// would clear whatever database the first was using. Two process ids sixteen
// apart still collide; that window is small because it now takes ids that line
// up rather than every pair of binaries.
var nextDB = func() *atomic.Int32 {
	seeded := &atomic.Int32{}
	//nolint:gosec // a process id is small next to int32 by construction
	seeded.Store(int32(os.Getpid()))
	return seeded
}()

// Acquire returns a server address and a database index on it that no other
// caller is using, with that database empty.
//
// The caller does not close anything: the container outlives the test run, and
// the database is cleared by the next Acquire that takes it.
func Acquire(t *testing.T) (addr string, db int) {
	t.Helper()
	requireDocker(t)
	addr = ensureServer(t)
	db = int(nextDB.Add(1)-1) % databases

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer func() { _ = client.Close() }()
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("redistest: clearing database %d: %v", db, err)
	}
	return addr, db
}

// requireDocker fails the case when there is no docker to drive.
//
// Under CI the failure is unconditional: a case that silently skips where it is
// meant to run is not a gate, and these cases are the only place the two
// implementations meet a real server.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("redistest: docker is required in CI and was not found: %v", err)
		}
		t.Skip("redistest: docker is not installed, and the cases that need a real Redis are skipped")
	}
}

// ensureServer returns the address of the shared server, starting it when it is
// not already running.
func ensureServer(t *testing.T) string {
	t.Helper()
	if !running(t) {
		// A container that is there but not running is started, not removed:
		// it is another test binary's container a moment before it comes up,
		// or an interrupted run's leftover, and removing it would reach into
		// the other binary's window and take its server with it.
		if exists(t) {
			if _, err := docker(t, "start", containerName); err != nil && !running(t) {
				t.Fatalf("redistest: starting the existing %s container: %v", containerName, err)
			}
		} else if createErr := create(t); createErr != nil {
			// The create can lose to another test binary starting at the same
			// moment: docker refuses the duplicate name, and it takes the name
			// before the container can be looked at, so the loser spends a
			// window being told there is no such container and only then finds
			// the winner's. The wait below is that window, and the container it
			// ends on is the winner's; a losing process never creates one of
			// its own.
			deadline := time.Now().Add(bringUpBound)
			for time.Now().Before(deadline) {
				if running(t) {
					break
				}
				time.Sleep(bringUpInterval)
			}
			if !running(t) {
				t.Fatalf("redistest: the %s container did not come up within %s: create failed with %v",
					containerName, bringUpBound, createErr)
			}
		}
	}
	waitReady(t)
	return publishedAddr(t)
}

// create starts the container. It reports docker's refusal as an error rather
// than failing the case, because the caller has to decide whether the refusal
// was the race above.
func create(t *testing.T) error {
	t.Helper()
	_, err := docker(t, "run", "-d", "--name", containerName, "-p", "0:6379", image)
	return err
}

// running reports whether the named container is up.
func running(t *testing.T) bool {
	t.Helper()
	out, err := docker(t, "inspect", "-f", "{{.State.Running}}", containerName)
	return err == nil && strings.TrimSpace(out) == runningState
}

// exists reports whether a container with the name is present at all.
func exists(t *testing.T) bool {
	t.Helper()
	_, err := docker(t, "inspect", "-f", "{{.Name}}", containerName)
	return err == nil
}

// publishedAddr reads the host port docker mapped the server's own port onto.
func publishedAddr(t *testing.T) string {
	t.Helper()
	out, err := docker(t, "port", containerName, "6379")
	if err != nil {
		t.Fatalf("redistest: reading the published port: %v", err)
	}
	// "0.0.0.0:32768" or "[::]:32768" — the last colon separates the port.
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	i := strings.LastIndex(line, ":")
	if i < 0 {
		t.Fatalf("redistest: docker port printed %q, which names no port", out)
	}
	port, err := strconv.Atoi(line[i+1:])
	if err != nil {
		t.Fatalf("redistest: docker port printed %q, whose port is not a number", out)
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// waitReady polls the server until it answers, so a case never races the
// container's own startup.
func waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	addr := publishedAddr(t)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		client := redis.NewClient(&redis.Options{Addr: addr})
		last = client.Ping(ctx).Err()
		_ = client.Close()
		cancel()
		if last == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("redistest: the server at %s did not answer within %s: %v", addr, readyTimeout, last)
}

// docker runs one docker invocation and returns its standard output.
func docker(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	//nolint:gosec // every argument is one of this file's own constants or a container name built from them; nothing a caller supplies reaches the command line
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.Output()
	return string(out), err
}
