// Package memcachetest hands the tests a Memcached server to run against, and
// every case a key space of its own on it.
//
// The server is the one the repository's compose stack brings up, at the address
// SPEED_TEST_MEMCACHED_ADDR names and at loopback when it is unset; this package
// starts nothing and manages no container. That
// the stack is the only thing that starts it is what keeps the server's version
// in one place instead of in the fixture.
//
// A machine that cannot reach the server fails these cases rather than skipping
// them: a gate that is silently skipped wherever it is actually meant to run is
// not a gate. The failure names the entry point that brings the stack up.
//
// Memcached has no logical databases, so isolation is by key space rather than
// by server. Space hands each case a prefix no other caller is handed, the
// implementation writes it in front of every key's spelling, and nothing
// empties the server — a flush takes every entry out from under whichever case
// another binary is running at that moment, and that case fails as a key that
// vanished mid-case.
//
// The entries a case leaves behind are never read again. The server evicts
// them as its memory fills, least recently used first, so they are the ones it
// drops before anything a running case has just written.
//
// Only this module's Memcached implementation uses this server.
package memcachetest

import (
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

const (
	// defaultAddr is where the compose stack publishes Memcached by default. A
	// deployment that runs the stack elsewhere -- another compose project, or
	// the same one with other published ports -- points the tests at its
	// address with SPEED_TEST_MEMCACHED_ADDR; an empty variable means this one.
	defaultAddr = "127.0.0.1:11211"
	// readyTimeout bounds the wait for the server to accept connections. The
	// stack is normally already healthy by the time a test binary starts --
	// `make deps-up` waits for its healthchecks -- so this window is for the
	// run that follows a bare `docker compose up -d`.
	readyTimeout = 30 * time.Second
	// readyInterval is how often the wait looks while it waits.
	readyInterval = 150 * time.Millisecond
	// spaceEnd closes every space. Memcached accepts it in a key, base32 does
	// not contain it, and no spelling the implementation makes contains it.
	spaceEnd = "."
)

// address is where the tests reach Memcached: defaultAddr unless
// SPEED_TEST_MEMCACHED_ADDR names another address.
//
// It is read on each call rather than once at start-up, so a case that sets the
// variable observes the address it set.
func address() string { return envOr("SPEED_TEST_MEMCACHED_ADDR", defaultAddr) }

// envOr returns the value of the named environment variable, or fallback when
// it is unset or empty.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Addr returns the address of the shared server, failing the case when nothing
// answers there.
//
// The server holds every other case's entries too, and it is never emptied: a
// case works in a Space of its own instead.
func Addr(t *testing.T) string {
	t.Helper()
	if err := waitReady(); err != nil {
		t.Fatalf("memcachetest: the Memcached test server at %s did not accept a connection (the wait is bounded at %s): %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", address(), readyTimeout, err)
	}
	return address()
}

// Space returns a key space for one case: a prefix no other call returns, in
// this test binary or in any other on the machine.
//
// A space is random base32 text closed by a dot, and that dot is the only one
// in it. The name of an entry therefore says where its space ends, at its
// first dot, so two spaces never claim one name. A name with no dot is in no
// space at all, and that is every name the implementation makes for a store
// with no space: base64url has no dot, and the digest's prefix ends in a
// colon. Stores in two spaces therefore never meet on an entry, and neither do
// a store in a space and one without.
func Space(t *testing.T) string {
	t.Helper()
	return rand.Text() + spaceEnd
}

// waitReady dials the server until it accepts a connection, the dial fails for
// a reason the wait cannot clear, or the bound is up.
//
// A dial is enough here: Memcached answers no greeting and has no ping, and a
// server that accepts a TCP connection is one the client can write to.
func waitReady() error {
	return waitReadyUntil(time.Now().Add(readyTimeout), readyInterval, func() error {
		conn, err := net.DialTimeout("tcp", address(), 2*time.Second)
		if err == nil {
			_ = conn.Close()
		}
		return err
	})
}

// waitReadyUntil runs attempt until it succeeds, returns an error that is not
// about the connection, or reaches the deadline.
//
// A connection-level failure is what a server that is still starting answers
// with — the published port is not listening yet, or it takes the connection
// and closes it before it is serving — so it is retried. Anything else is a
// dial that will not become ready however long the wait runs, and it is
// reported at once. attempt and interval are parameters so the loop can be
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
// rather than "any error from a dial": a dial reports a malformed address or an
// unresolvable host as a *net.OpError too, and neither becomes reachable by
// waiting.
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
