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
// binary. Isolation is per database index, not per container: Acquire claims one
// of the databases a default Redis has and clears it, because go test runs the
// packages of one module in parallel and make check runs two legs — and two
// callers landing on one database have each of them clearing the other's work,
// which is a failure that shows up as a missing key rather than as a collision.
//
// Two modules use this — the key-value one and the event one — so it lives with
// the client wrapper they share, in pkg/internal/redis/redistest, inside the pkg
// module's internal/ tree: it is read by tests only, belongs to no public
// surface, and the compiler holds that, keeping anything outside the module from
// importing it. See docs/adr/adr-shared-backend-unit-2026-09-21.md.
package redistest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	// assert on which keys exist. One of them holds the leases and is never
	// handed out, so this many databases means one fewer a case can run in.
	databases = 16
	// leaseDB is the database the claims live in. It is outside the range
	// Acquire hands out, so no case ever clears it.
	leaseDB = databases - 1
	// leaseTTL bounds how long a claim lives. It is far longer than any case
	// here, and short enough that a binary killed mid-case does not hold a
	// database for the rest of the machine's uptime.
	leaseTTL = 15 * time.Minute
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

// acquireTimeout bounds the wait for a database to come free. A machine
// running several test binaries at once can have every one of them taken at
// the moment one asks, and waiting is the answer rather than failing: the
// leases expire and the callers that hold them give them back when their cases
// end.
const acquireTimeout = 2 * time.Minute

// releaseTimeout bounds the call that gives a database back. It runs from a
// cleanup, where a failure has nowhere useful to go.
const releaseTimeout = 5 * time.Second

// Acquire returns a server address and a database index on it that no other
// caller is using, with that database empty.
//
// The caller does not close anything: the container outlives the test run, and
// the database is cleared by the next Acquire that takes it. The index is
// given back when the case ends.
func Acquire(t *testing.T) (addr string, db int) {
	t.Helper()
	requireDocker(t)
	addr = ensureServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db = claim(t, ctx, addr)

	t.Cleanup(func() { release(addr, db, t) })

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	defer func() { _ = client.Close() }()
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("redistest: clearing database %d: %v", db, err)
	}
	return addr, db
}

// claim takes a database nobody else holds and returns its index.
//
// The claim is a key written with NX into the lease database, which is outside
// the range handed out here and so is never cleared by a case. That is what
// makes the twenty-odd tests in two packages, running as separate binaries at
// once, land on twenty-odd databases without any of them clearing another's
// work: the write is the arbitration, and it is atomic on the server.
//
// A lease carries a bound on its life so that a binary killed mid-case does
// not hold a database for the rest of the machine's uptime. The bound is far
// longer than any case here.
func claim(t *testing.T, ctx context.Context, addr string) int {
	t.Helper()

	deadline := time.Now().Add(acquireTimeout)
	for {
		db, err := claimFor(ctx, addr, leaseOwner())
		if err != nil {
			t.Fatalf("redistest: claiming a database: %v", err)
		}
		if db >= 0 {
			return db
		}
		if time.Now().After(deadline) {
			t.Fatalf("redistest: every one of the %d databases has been claimed for %v, "+
				"which means a case is holding one far longer than any case here does",
				databases-1, acquireTimeout)
		}
		time.Sleep(bringUpInterval)
	}
}

// claimFor takes the first free database for owner, or reports -1 when they
// are all taken.
//
// The arbitration is the SetNX itself: it is atomic on the server, so two
// callers asking at the same instant in different processes cannot both be
// told they hold the same index. Nothing about the caller enters into it,
// which is the point — an index derived from the caller's identity would put
// two test binaries on one database as soon as their identities lined up.
func claimFor(ctx context.Context, addr, owner string) (int, error) {
	client := redis.NewClient(&redis.Options{Addr: addr, DB: leaseDB})
	defer func() { _ = client.Close() }()

	for n := range databases - 1 {
		ok, err := client.SetNX(ctx, leaseKey(n), owner, leaseTTL).Result()
		if err != nil {
			return 0, err
		}
		if ok {
			return n, nil
		}
	}
	return -1, nil
}

// release gives a database back. A failure is reported and not fatal: the
// lease expires on its own, and a cleanup that fails should not turn a passing
// case red.
func release(addr string, db int, t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, DB: leaseDB})
	defer func() { _ = client.Close() }()
	if err := client.Del(ctx, leaseKey(db)).Err(); err != nil {
		t.Logf("redistest: giving database %d back: %v", db, err)
	}
}

// leaseOwner identifies the holder of a lease, for a reader looking at the
// lease database while a case is running.
func leaseOwner() string {
	//nolint:gosec // a process id is small next to int32 by construction
	return "pid:" + strconv.Itoa(os.Getpid())
}

// leaseKey is the key a claim for one database is written under.
func leaseKey(n int) string { return "redistest:lease:" + strconv.Itoa(n) }

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
