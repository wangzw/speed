// Package natstest hands a test the shared NATS server, with JetStream enabled, at the address SPEED_TEST_NATS_URL names.
package natstest

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	defaultURL    = "nats://127.0.0.1:4222"
	readyTimeout  = 30 * time.Second
	readyInterval = 150 * time.Millisecond
)

func serverURL() string { return envOr("SPEED_TEST_NATS_URL", defaultURL) }

func dialAddress() string { return strings.TrimPrefix(serverURL(), "nats://") }

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// URL returns the address of a server with JetStream enabled, failing the case when nothing
// answers there.
func URL(t *testing.T) string {
	t.Helper()
	if err := waitReady(); err != nil {
		t.Fatalf("natstest: the NATS test server at %s did not accept a connection (the wait is bounded at %s): %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", dialAddress(), readyTimeout, err)
	}
	return serverURL()
}

func waitReady() error {
	return waitReadyUntil(time.Now().Add(readyTimeout), readyInterval, func() error {
		conn, err := net.DialTimeout("tcp", dialAddress(), 2*time.Second)
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
