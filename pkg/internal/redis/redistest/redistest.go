// Package redistest hands a test the shared Redis server and a logical database no other caller holds.
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
	defaultAddr   = "127.0.0.1:6379"
	databases     = 16
	leaseDB       = databases - 1
	leaseTTL      = 15 * time.Minute
	readyTimeout  = 30 * time.Second
	readyInterval = 150 * time.Millisecond
	claimInterval = 150 * time.Millisecond
)

const acquireTimeout = 2 * time.Minute

const releaseTimeout = 5 * time.Second

func address() string { return envOr("SPEED_TEST_REDIS_ADDR", defaultAddr) }

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Acquire returns a server address and a database index on it that no other caller is using, with
// that database empty.
// It fails the case when the server does not answer.
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

func leaseOwner() string {
	//nolint:gosec // a process id is small next to int32 by construction
	return "pid:" + strconv.Itoa(os.Getpid())
}

func leaseKey(n int) string { return "redistest:lease:" + strconv.Itoa(n) }

func requireServer(t *testing.T) {
	t.Helper()
	if err := waitReady(); err != nil {
		t.Fatalf("redistest: the Redis test server at %s did not answer (the wait is bounded at %s): %v\n"+
			"run `make deps-up` from the repository root to bring the stack up", address(), readyTimeout, err)
	}
}

func waitReady() error {
	return waitReadyUntil(time.Now().Add(readyTimeout), readyInterval, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		client := redis.NewClient(&redis.Options{Addr: address()})
		defer func() { _ = client.Close() }()
		return client.Ping(ctx).Err()
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
