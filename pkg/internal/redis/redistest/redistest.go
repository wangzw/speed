// Package redistest hands the tests a Redis server to run against, and gives
// every caller a database of its own on it.
//
// The server is the one the repository's compose stack brings up, at the address
// SPEED_TEST_REDIS_ADDR names and at loopback when it is unset; this package
// starts nothing and manages no container. What
// it owns is everything after the server is up: choosing a logical database no
// other caller holds, clearing it, and giving it back when the case ends.
//
// Isolation is per database index, not per server: Acquire claims one of the
// databases a default Redis has and clears it, because go test runs the
// packages of one module in parallel and the integration suite runs under the
// race detector too — and two callers landing on one database have each of them
// clearing the other's work, which is a failure that shows up as a missing key
// rather than as a collision.
//
// A machine that cannot reach the server fails these cases rather than skipping
// them: a gate that is silently skipped wherever it is actually meant to run is
// not a gate. The failure names the entry point that brings the stack up.
//
// Two modules use this — the key-value one and the event one — so it lives with
// the client wrapper they share, in pkg/internal/redis/redistest, inside the pkg
// module's internal/ tree: it is read by tests only, belongs to no public
// surface, and the compiler holds that, keeping anything outside the module from
// importing it. See docs/adr/adr-shared-backend-unit-2026-09-21.md.
package redistest

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// defaultAddr is where the compose stack publishes Redis by default. A
	// deployment that runs the stack elsewhere -- another compose project, or
	// the same one with other published ports -- points the tests at its
	// address with SPEED_TEST_REDIS_ADDR; an empty variable means this one.
	defaultAddr = "127.0.0.1:6379"
	// databases is how many logical databases a default Redis offers. They are
	// the unit of isolation here; a key prefix would not do, because some cases
	// assert on which keys exist. One of them holds the leases and is never
	// handed out, so this many databases means one fewer a case can run in.
	databases = 16
	// leaseDB is the database the claims live in. It is outside the range
	// Acquire hands out, so no case ever clears it.
	leaseDB = databases - 1
	// leaseTTL bounds how long a claim lives. The stack outlives every process
	// that borrows it, so without a bound a binary killed mid-case would hold a
	// database until the stack was torn down. The bound is far longer than any
	// case here.
	leaseTTL = 15 * time.Minute
	// readyTimeout bounds the wait for the server to answer, and readyInterval
	// is how often the wait looks while it does. The stack is normally already
	// healthy by the time a test binary starts -- `make deps-up` waits for its
	// healthchecks -- so this window is for the run that follows a bare
	// `docker compose up -d`.
	readyTimeout  = 30 * time.Second
	readyInterval = 150 * time.Millisecond
	// claimInterval is how often a claim looks again while every database is
	// taken.
	claimInterval = 150 * time.Millisecond
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

// address is where the tests reach Redis: defaultAddr unless
// SPEED_TEST_REDIS_ADDR names another address.
//
// It is read on each call rather than once at start-up, so a case that sets the
// variable observes the address it set.
func address() string { return envOr("SPEED_TEST_REDIS_ADDR", defaultAddr) }

// envOr returns the value of the named environment variable, or fallback when
// it is unset or empty.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Acquire returns a server address and a database index on it that no other
// caller is using, with that database empty.
//
// It fails the case when the server does not answer.
//
// The caller does not close anything: the server outlives the test run, and the
// database is cleared by the next Acquire that takes it. The index is given
// back when the case ends.
func Acquire(t *testing.T) (addr string, db int) {
	t.Helper()
	requireServer(t)
	addr = address()

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
// not hold a database until the stack is torn down.
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
		time.Sleep(claimInterval)
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

// requireServer fails the case when the server does not answer, naming the
// entry point that brings the stack up: a case that cannot reach the server is
// a stack that is not up far more often than it is a broken server.
func requireServer(t *testing.T) {
	t.Helper()
	if err := waitReady(); err != nil {
		t.Fatalf("redistest: the Redis test server at %s did not answer (the wait is bounded at %s): %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", address(), readyTimeout, err)
	}
}

// waitReady pings the server until it answers, the ping fails for a reason the
// wait cannot clear, or the bound is up.
func waitReady() error {
	return waitReadyUntil(time.Now().Add(readyTimeout), readyInterval, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		client := redis.NewClient(&redis.Options{Addr: address()})
		defer func() { _ = client.Close() }()
		return client.Ping(ctx).Err()
	})
}

// waitReadyUntil runs attempt until it succeeds, returns an error that is not
// about the connection, or reaches the deadline.
//
// A connection-level failure is what a server that is still starting answers
// with — the published port is not listening yet, or it takes the connection
// and closes it before it is serving — so it is retried. Anything else is a
// server that answered but is not going to start answering differently, and it
// is reported at once. attempt and interval are parameters so the loop can be
// exercised without a server.
func waitReadyUntil(deadline time.Time, interval time.Duration, attempt func() error) error {
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

// retryableConnectionError reports whether err is a failure of the connection
// itself, the kind a server that is still coming up produces before it serves.
//
// It is an allowlist of the kernel's connection-level refusals and timeouts
// rather than "any error from a ping": the client reports an authentication
// failure or a malformed address through the same call, and neither becomes
// ready by waiting. A server reply — including one that says the server is
// still loading its dataset — is a reply rather than a failed connection, and
// it ends the wait.
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
	return errors.As(err, &timeout) && timeout.Timeout()
}
