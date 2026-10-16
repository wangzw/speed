// Package natstest hands the tests a NATS server to run against.
//
// The server is the one the repository's compose stack brings up, with
// JetStream enabled, at a fixed loopback address; this package starts nothing
// and manages no container. That the stack is the only thing that starts it is
// what keeps the server's version and its flags in one place instead of in
// every fixture that needs a server.
//
// A machine that cannot reach the server fails these cases rather than skipping
// them: a gate that is silently skipped wherever it is actually meant to run is
// not a gate. The failure names the entry point that brings the stack up.
//
// Isolation is by bucket or stream name rather than by server: JetStream has no
// logical databases, and the cases name their own so that two of them on one
// server cannot see each other's entries.
//
// The key-value and event modules' NATS backends both test against this one, so
// it stands as a shared unit of its own in pkg/internal/natstest, inside the
// pkg module's internal/ tree: it is read by tests only, belongs to no public
// surface, and the compiler holds that, keeping anything outside the module
// from importing it. The pool-based fixture lives in pkg/internal/pgpool/pgtest,
// inside the pool wrapper's directory, and the record behind both is
// docs/adr/adr-shared-backend-unit-2026-09-21.md.
package natstest

import (
	"net"
	"testing"
	"time"
)

const (
	// url is where the compose stack publishes NATS. It is fixed because
	// nothing here discovers a port any more, and JetStream is on there: both
	// the key-value store and the event bus are built on it, and a server
	// started without it answers neither.
	url = "nats://127.0.0.1:4222"
	// addr is url without the scheme, which is what a dial takes.
	addr = "127.0.0.1:4222"
	// readyTimeout bounds the wait for the server to accept connections. The
	// stack is normally already healthy by the time a test binary starts --
	// `make deps-up` waits for its healthchecks -- so this window is for the
	// run that follows a bare `docker compose up -d`.
	readyTimeout = 30 * time.Second
	// readyInterval is how often the wait looks while it waits.
	readyInterval = 150 * time.Millisecond
)

// URL returns the address of a server with JetStream enabled, failing the case
// when nothing answers there.
func URL(t *testing.T) string {
	t.Helper()
	if err := waitReady(); err != nil {
		t.Fatalf("natstest: the NATS test server at %s did not accept a connection within %s: %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", addr, readyTimeout, err)
	}
	return url
}

// waitReady dials the server until it accepts a connection or the bound is up.
//
// A dial is enough here: the addresses are what the cases go on to use, and a
// server that accepts a TCP connection is one a client can handshake with.
func waitReady() error {
	deadline := time.Now().Add(readyTimeout)
	for {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(readyInterval)
	}
}
