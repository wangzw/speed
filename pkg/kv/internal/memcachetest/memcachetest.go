// Package memcachetest hands a test the shared Memcached server and a key space no other caller holds.
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
	defaultAddr   = "127.0.0.1:11211"
	readyTimeout  = 30 * time.Second
	readyInterval = 150 * time.Millisecond
	spaceEnd      = "."
)

func address() string { return envOr("SPEED_TEST_MEMCACHED_ADDR", defaultAddr) }

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Addr returns the address of the shared server, failing the case when nothing answers there.
// The server holds every other case's entries too, and it is never emptied: a case works in a
// Space of its own instead.
func Addr(t *testing.T) string {
	t.Helper()
	if err := waitReady(); err != nil {
		t.Fatalf("memcachetest: the Memcached test server at %s did not accept a connection (the wait is bounded at %s): %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", address(), readyTimeout, err)
	}
	return address()
}

// Space returns a key space for one case: a prefix no other call returns, in this test binary or
// in any other on the machine.
// A space is random base32 text closed by a dot, and that dot is the only one in it.
func Space(t *testing.T) string {
	t.Helper()
	return rand.Text() + spaceEnd
}

func waitReady() error {
	return waitReadyUntil(time.Now().Add(readyTimeout), readyInterval, func() error {
		conn, err := net.DialTimeout("tcp", address(), 2*time.Second)
		if err == nil {
			_ = conn.Close()
		}
		return err
	})
}

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
